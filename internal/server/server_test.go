package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	addr, stop := startServer(t, &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	defer stop()

	first := roundTrip(t, addr, "GET /a HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if !strings.Contains(first, "GET /a HTTP/1.1\n") {
		t.Fatalf("first body %q", first)
	}
	second := roundTrip(t, addr, "POST /b HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\n\r\nxyz")
	if !strings.Contains(second, "POST /b HTTP/1.1\n") || !strings.HasSuffix(second, "\nxyz") {
		t.Fatalf("second body %q", second)
	}
}

func TestSecondClientWaitsForTheFirst(t *testing.T) {
	var once sync.Once
	accepted := make(chan struct{})
	addr, stop := startServer(t, &Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		onAccepted: func(net.Conn) {
			once.Do(func() { close(accepted) })
		},
	})
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
		t.Fatalf("second client was served while the first connection was open: %q", buf[:n])
	} else if !isTimeout(err) {
		t.Fatalf("second client read: %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	body := readResponseBody(t, second)
	if !strings.Contains(body, "GET /ready HTTP/1.1\n") {
		t.Fatalf("second body %q", body)
	}
}

func TestFragmentedRequest(t *testing.T) {
	addr, stop := startServer(t, &Server{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
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
	if !strings.Contains(body, "GET /hello HTTP/1.1\n") || !strings.Contains(body, "Host: localhost\n") {
		t.Fatalf("body %q", body)
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
	buf := make([]byte, 8)
	n, err := conn.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read %d bytes, err %v; want EOF", n, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logBuf.String(), "request line exceeds maximum size") {
		if time.Now().After(deadline) {
			t.Fatalf("log %q", logBuf.String())
		}
		time.Sleep(10 * time.Millisecond)
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

func roundTrip(t *testing.T, addr, raw string) string {
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
	return readResponseBody(t, conn)
}

func readResponseBody(t *testing.T, r io.Reader) string {
	t.Helper()
	br := bufio.NewReader(r)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if status != "HTTP/1.1 200 OK\r\n" {
		t.Fatalf("status %q", status)
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
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
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
