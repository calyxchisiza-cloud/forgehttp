package server

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"forgehttp/internal/protocol/http1"
	"forgehttp/internal/transport"
)

func TestTwoClientsSequentially(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}
	registerHello(t, srv)
	addr, stop := startServer(t, srv)
	defer stop()

	first := roundTrip(t, addr, "GET /a HTTP/1.1\r\nHost: localhost\r\n\r\n")
	second := roundTrip(t, addr, "POST /b HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\n\r\nxyz")
	if first != http1.HelloMessage || second != http1.HelloMessage {
		t.Fatalf("bodies %q and %q", first, second)
	}
}

func TestSecondClientServedWhileFirstIsOpen(t *testing.T) {
	var once sync.Once
	accepted := make(chan struct{})
	srv := &Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		onAccepted: func(net.Conn) {
			once.Do(func() { close(accepted) })
		},
	}
	registerHello(t, srv)
	addr, stop := startServer(t, srv)
	defer stop()

	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.Write([]byte("GET /")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("first connection was not accepted")
	}

	body := roundTrip(t, addr, "GET /ready HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if body != http1.HelloMessage {
		t.Fatalf("second body %q", body)
	}
}

func TestConnectionLimitHoldsFurtherClients(t *testing.T) {
	var once sync.Once
	accepted := make(chan struct{})
	srv := &Server{
		MaxConns:     1,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		onAccepted: func(net.Conn) {
			once.Do(func() { close(accepted) })
		},
	}
	registerHello(t, srv)
	addr, stop := startServer(t, srv)
	defer stop()

	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.Write([]byte("GET /")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("first connection was not accepted")
	}

	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Write([]byte("GET /ready HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 32)
	if n, err := second.Read(buf); err == nil {
		t.Fatalf("client past the connection limit was served: %q", buf[:n])
	} else if !isTimeout(err) {
		t.Fatalf("second client read: %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	body := readResponseBody(t, second)
	if body != http1.HelloMessage {
		t.Fatalf("second body %q", body)
	}
}

func TestHandlersOverlapAndKeepSharedState(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	free := func() { releaseOnce.Do(func() { close(release) }) }
	defer free()
	var mu sync.Mutex
	var n int
	srv := &Server{
		MaxConns:     2,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}
	if err := srv.Handle("GET", "/n", func(*http1.Request) *http1.Response {
		mu.Lock()
		n++
		current := n
		mu.Unlock()
		if current <= 2 {
			started <- struct{}{}
			<-release
		}
		return http1.Text(200, strconv.Itoa(current))
	}); err != nil {
		t.Fatal(err)
	}
	addr, stop := startServer(t, srv)
	defer stop()

	const total = 6
	type result struct {
		body string
		err  error
	}
	results := make(chan result, total)
	for i := 0; i < total; i++ {
		go func() {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := conn.Write([]byte("GET /n HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
				results <- result{err: err}
				return
			}
			_, body, _, err := readResponseErr(conn)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{body: body}
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("handlers did not run at the same time")
		}
	}
	free()

	seen := map[string]struct{}{}
	for i := 0; i < total; i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		seen[got.body] = struct{}{}
	}
	if len(seen) != total {
		t.Fatalf("shared counter produced %d distinct bodies: %v", len(seen), seen)
	}
	mu.Lock()
	gotN := n
	mu.Unlock()
	if gotN != total {
		t.Fatalf("counter %d", gotN)
	}
}

func TestRequestsOnOneConnectionStayInOrder(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second}
	registerText(t, srv, "GET", "/one", "one")
	registerText(t, srv, "GET", "/two", "two")
	addr, stop := startServer(t, srv)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	raw := "GET /one HTTP/1.1\r\nHost: localhost\r\n\r\n" +
		"GET /two HTTP/1.1\r\nHost: localhost\r\n\r\n"
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	status, connection, body := readMessage(t, conn)
	if status != "HTTP/1.1 200 OK\r\n" || connection != "keep-alive" || body != "one" {
		t.Fatalf("first status %q connection %q body %q", status, connection, body)
	}
	status, connection, body = readMessage(t, conn)
	if status != "HTTP/1.1 200 OK\r\n" || connection != "keep-alive" || body != "two" {
		t.Fatalf("second status %q connection %q body %q", status, connection, body)
	}
}

func TestHTTP10ClosesUnlessKeepAlive(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second}
	registerText(t, srv, "GET", "/one", "one")
	registerText(t, srv, "GET", "/two", "two")
	addr, stop := startServer(t, srv)
	defer stop()

	closed, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer closed.Close()
	_ = closed.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := closed.Write([]byte("GET /one HTTP/1.0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_, connection, body := readMessage(t, closed)
	if connection != "close" || body != "one" {
		t.Fatalf("HTTP/1.0 connection %q body %q", connection, body)
	}
	_ = closed.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := closed.Read(make([]byte, 8)); err == nil || isTimeout(err) {
		t.Fatalf("HTTP/1.0 connection stayed open: %d %v", n, err)
	}

	kept, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	_ = kept.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := kept.Write([]byte("GET /one HTTP/1.0\r\nConnection: keep-alive\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_, connection, body = readMessage(t, kept)
	if connection != "keep-alive" || body != "one" {
		t.Fatalf("HTTP/1.0 keep-alive connection %q body %q", connection, body)
	}
	if _, err := kept.Write([]byte("GET /two HTTP/1.0\r\nConnection: keep-alive\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	_, connection, body = readMessage(t, kept)
	if connection != "keep-alive" || body != "two" {
		t.Fatalf("second HTTP/1.0 connection %q body %q", connection, body)
	}
}

func TestConnectionCloseEndsTheConnection(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second}
	registerText(t, srv, "GET", "/one", "one")
	addr, stop := startServer(t, srv)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	raw := "GET /one HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive, close\r\n\r\n"
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	_, connection, body := readMessage(t, conn)
	if connection != "close" || body != "one" {
		t.Fatalf("connection %q body %q", connection, body)
	}
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 8)); err == nil || isTimeout(err) {
		t.Fatalf("connection stayed open after Connection: close: %d %v", n, err)
	}
}

func TestIdleTimeoutClosesQuietly(t *testing.T) {
	srv := &Server{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 2 * time.Second,
		IdleTimeout:  200 * time.Millisecond,
	}
	addr, stop := startServer(t, srv)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := conn.Read(make([]byte, 8)); err == nil || isTimeout(err) {
		t.Fatalf("idle connection was not closed: %d %v", n, err)
	}
}

func TestSlowRequestTimesOut(t *testing.T) {
	srv := &Server{
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 2 * time.Second,
		IdleTimeout:  2 * time.Second,
	}
	addr, stop := startServer(t, srv)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET /")); err != nil {
		t.Fatal(err)
	}
	status, connection, body := readMessage(t, conn)
	if status != "HTTP/1.1 408 Request Timeout\r\n" || connection != "close" || body != "request timeout" {
		t.Fatalf("status %q connection %q body %q", status, connection, body)
	}
}

func TestShutdownFinishesCurrentResponse(t *testing.T) {
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		IdleTimeout:  2 * time.Second,
		Logger:       log.New(io.Discard, "", 0),
	}
	if err := srv.Handle("GET", "/hold", func(*http1.Request) *http1.Response {
		close(started)
		<-release
		return http1.Text(200, "done")
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET /hold HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	close(release)
	status, connection, body := readMessage(t, conn)
	if status != "HTTP/1.1 200 OK\r\n" || connection != "close" || body != "done" {
		t.Fatalf("status %q connection %q body %q", status, connection, body)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish the in-flight response")
	}
}

func TestSnapshotCountsAKeptConnection(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second}
	registerText(t, srv, "GET", "/one", "one")
	addr, stop := startServer(t, srv)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET /one HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, body := readMessage(t, conn); body != "one" {
		t.Fatalf("body %q", body)
	}
	deadline := time.Now().Add(time.Second)
	var snap Snapshot
	for {
		snap = srv.Snapshot()
		ready := len(snap.Traces) == 1 && snap.Traces[0].Status == 200 && snap.Traces[0].Connection == "keep-alive"
		for _, c := range snap.Connections {
			if ready && c.Requests == 1 && c.Stage == "reading" {
				if snap.MaxConns != 100 || snap.Active < 1 {
					t.Fatalf("snapshot %+v", snap)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot %+v", snap)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFragmentedRequest(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}
	registerHello(t, srv)
	addr, stop := startServer(t, srv)
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET /hello HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("Host: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, conn)
	if body != http1.HelloMessage || len(body) != 34 {
		t.Fatalf("body %q (%d bytes)", body, len(body))
	}
}

func TestOversizedRequestLineClosesConnection(t *testing.T) {
	var logBuf lockedBuffer
	addr, stop := startServer(t, &Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		Limits:       http1.Limits{MaxRequestLine: 64, MaxHeaders: 128, MaxBody: 32},
		Logger:       log.New(&logBuf, "", 0),
	})
	defer stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(bytes.Repeat([]byte("A"), 64)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	status, body, n := readResponse(t, conn)
	if status != "HTTP/1.1 431 Request Header Fields Too Large\r\n" || body != "headers too large" || n != len(body) {
		t.Fatalf("status %q body %q length %d", status, body, n)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logBuf.String(), "request line exceeds maximum size") {
		if time.Now().After(deadline) {
			t.Fatalf("log %q", logBuf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRoutesAndRequestContext(t *testing.T) {
	srv := &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}
	if err := srv.Handle("GET", "/ctx", func(req *http1.Request) *http1.Response {
		if req.Context().Err() != nil {
			return http1.Text(500, "cancelled")
		}
		return http1.Text(200, "ok")
	}); err != nil {
		t.Fatal(err)
	}
	addr, stop := startServer(t, srv)
	defer stop()

	if got := roundTrip(t, addr, "GET /ctx HTTP/1.1\r\nHost: localhost\r\n\r\n"); got != "ok" {
		t.Fatalf("context handler body %q", got)
	}
	missing := roundTripStatus(t, addr, "GET /missing HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if missing.status != "HTTP/1.1 404 Not Found\r\n" || missing.body != "not found" {
		t.Fatalf("missing %#v", missing)
	}
	wrong := roundTripStatus(t, addr, "POST /ctx HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n")
	if wrong.status != "HTTP/1.1 405 Method Not Allowed\r\n" || wrong.body != "method not allowed" {
		t.Fatalf("method %#v", wrong)
	}
}

func TestBadRequestAndChunkedGetErrorResponses(t *testing.T) {
	addr, stop := startServer(t, &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	defer stop()

	missingHost := roundTripStatus(t, addr, "GET / HTTP/1.1\r\n\r\n")
	if missingHost.status != "HTTP/1.1 400 Bad Request\r\n" || missingHost.body != "bad request" {
		t.Fatalf("missing host %#v", missingHost)
	}
	chunked := roundTripStatus(t, addr, "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n")
	if chunked.status != "HTTP/1.1 501 Not Implemented\r\n" || chunked.body != "transfer encoding not implemented" {
		t.Fatalf("chunked %#v", chunked)
	}
	if strings.Contains(chunked.body, "hello") {
		t.Fatalf("chunked body was echoed: %q", chunked.body)
	}
}

func TestShutdownUnblocksReadAndRebinds(t *testing.T) {
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accepted := make(chan struct{})
	var once sync.Once
	srv := &Server{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 2 * time.Second,
		Logger:       log.New(io.Discard, "", 0),
		onAccepted: func(net.Conn) {
			once.Do(func() { close(accepted) })
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("connection was not accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not unblock the in-flight read")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 8)
	if _, err := conn.Read(buf); err == nil || isTimeout(err) {
		t.Fatalf("in-flight connection was not closed: %v", err)
	}
	rebound, err := listenAgain(addr)
	if err != nil {
		t.Fatal(err)
	}
	rebound.Close()
}

func TestListenAndServeCancel(t *testing.T) {
	var logBuf lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{
		Addr:         "127.0.0.1:0",
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		Logger:       log.New(&logBuf, "", 0),
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.ListenAndServe(ctx)
	}()
	addr := waitListenAddr(t, &logBuf)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}
}

func registerText(t *testing.T, srv *Server, method, path, body string) {
	t.Helper()
	if err := srv.Handle(method, path, func(*http1.Request) *http1.Response {
		return http1.Text(200, body)
	}); err != nil {
		t.Fatal(err)
	}
}

func registerHello(t *testing.T, srv *Server) {
	t.Helper()
	h := func(*http1.Request) *http1.Response {
		return http1.JSON(200, []byte(http1.HelloMessage))
	}
	for _, path := range []string{"/a", "/b", "/ready", "/hello"} {
		for _, method := range []string{"GET", "POST"} {
			if err := srv.Handle(method, path, h); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func startServer(t *testing.T, srv *Server) (string, func()) {
	t.Helper()
	if srv.Logger == nil {
		srv.Logger = log.New(io.Discard, "", 0)
	}
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	}
	return ln.Addr().String(), stop
}

type httpResult struct {
	status string
	body   string
}

func roundTrip(t *testing.T, addr, raw string) string {
	t.Helper()
	return roundTripStatus(t, addr, raw).body
}

func roundTripStatus(t *testing.T, addr, raw string) httpResult {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	status, body, _ := readResponse(t, conn)
	return httpResult{status: status, body: body}
}

func readMessage(t *testing.T, r io.Reader) (status, connection, body string) {
	t.Helper()
	br := bufio.NewReader(r)
	var err error
	status, err = br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var contentLength int
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length: ")))
			if err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(line, "Connection: ") {
			connection = strings.TrimSpace(strings.TrimPrefix(line, "Connection: "))
			connection = strings.TrimSuffix(connection, "\r")
		}
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	return status, connection, string(buf)
}

func readResponseBody(t *testing.T, r io.Reader) string {
	t.Helper()
	status, body, n := readResponse(t, r)
	if status != "HTTP/1.1 200 OK\r\n" || n != len(body) {
		t.Fatalf("status %q body %q length %d", status, body, n)
	}
	return body
}

func readResponse(t *testing.T, r io.Reader) (status, body string, contentLength int) {
	t.Helper()
	status, body, contentLength, err := readResponseErr(r)
	if err != nil {
		t.Fatal(err)
	}
	return status, body, contentLength
}

func readResponseErr(r io.Reader) (status, body string, contentLength int, err error) {
	br := bufio.NewReader(r)
	status, err = br.ReadString('\n')
	if err != nil {
		return "", "", 0, err
	}
	for {
		line, readErr := br.ReadString('\n')
		if readErr != nil {
			return "", "", 0, readErr
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length: ")))
			if err != nil {
				return "", "", 0, err
			}
		}
	}
	buf := make([]byte, contentLength)
	if _, err = io.ReadFull(br, buf); err != nil {
		return "", "", 0, err
	}
	return status, string(buf), contentLength, nil
}

func waitListenAddr(t *testing.T, buf *lockedBuffer) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, after, ok := strings.Cut(buf.String(), "listening on "); ok {
			line, _, _ := strings.Cut(after, "\n")
			return strings.TrimSpace(line)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not listen")
	return ""
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func listenAgain(addr string) (net.Listener, error) {
	var last error
	deadline := time.Now().Add(2 * time.Second)
	for {
		ln, err := transport.Listen(addr)
		if err == nil {
			return ln, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, last
		}
		time.Sleep(20 * time.Millisecond)
	}
}
