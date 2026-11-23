package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"forgehttp/internal/server"
	"forgehttp/internal/transport"
)

func TestUserAPI(t *testing.T) {
	srv := &server.Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		Logger:       log.New(io.Discard, "", 0),
	}
	if err := register(srv, newUserStore()); err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	defer func() {
		cancel()
		<-done
	}()
	addr := ln.Addr().String()

	if status, body := exchange(t, addr, "GET /api/users HTTP/1.1\r\nHost: localhost\r\n\r\n"); status != "HTTP/1.1 200 OK\r\n" || body != "[]" {
		t.Fatalf("list status %q body %q", status, body)
	}
	created := "POST /api/users HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 14\r\n\r\n{\"name\":\"Ada\"}"
	if status, body := exchange(t, addr, created); status != "HTTP/1.1 201 Created\r\n" || body != `{"id":1,"name":"Ada"}` {
		t.Fatalf("create status %q body %q", status, body)
	}
	if status, body := exchange(t, addr, "GET /api/users?x=1 HTTP/1.1\r\nHost: localhost\r\n\r\n"); status != "HTTP/1.1 200 OK\r\n" || body != `[{"id":1,"name":"Ada"}]` {
		t.Fatalf("list after create status %q body %q", status, body)
	}
	bad := "POST /api/users HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\n{}"
	if status, body := exchange(t, addr, bad); status != "HTTP/1.1 400 Bad Request\r\n" || body != "name is required" {
		t.Fatalf("empty name status %q body %q", status, body)
	}
	if status, body := exchange(t, addr, "DELETE /api/users HTTP/1.1\r\nHost: localhost\r\n\r\n"); status != "HTTP/1.1 405 Method Not Allowed\r\n" || body != "method not allowed" {
		t.Fatalf("delete status %q body %q", status, body)
	}
	if status, body := exchange(t, addr, "GET /nope HTTP/1.1\r\nHost: localhost\r\n\r\n"); status != "HTTP/1.1 404 Not Found\r\n" || body != "not found" {
		t.Fatalf("missing status %q body %q", status, body)
	}
	if status, body := exchange(t, addr, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); status != "HTTP/1.1 200 OK\r\n" || body != "Welcome to ForgeHTTP" {
		t.Fatalf("welcome status %q body %q", status, body)
	}
}

func TestConcurrentCreates(t *testing.T) {
	srv := &server.Server{
		MaxConns:     2,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		Logger:       log.New(io.Discard, "", 0),
	}
	if err := register(srv, newUserStore()); err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	defer func() {
		cancel()
		<-done
	}()
	addr := ln.Addr().String()

	const n = 8
	created := "POST /api/users HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 14\r\n\r\n{\"name\":\"Ada\"}"
	ids := make(chan int, n)
	errc := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			status, body, err := exchangeErr(addr, created)
			if err != nil {
				errc <- err
				return
			}
			if status != "HTTP/1.1 201 Created\r\n" {
				errc <- errStatus(status, body)
				return
			}
			var got user
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				errc <- err
				return
			}
			ids <- got.ID
		}()
	}
	wg.Wait()
	close(errc)
	close(ids)
	for err := range errc {
		t.Fatal(err)
	}
	seen := map[int]struct{}{}
	for id := range ids {
		if id < 1 || id > n {
			t.Fatalf("id %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("created %d distinct users", len(seen))
	}
	status, body := exchange(t, addr, "GET /api/users HTTP/1.1\r\nHost: localhost\r\n\r\n")
	var listed []user
	if status != "HTTP/1.1 200 OK\r\n" || json.Unmarshal([]byte(body), &listed) != nil || len(listed) != n {
		t.Fatalf("list status %q body %q", status, body)
	}
}

type statusError struct {
	status string
	body   string
}

func (e statusError) Error() string {
	return e.status + " " + e.body
}

func errStatus(status, body string) error {
	return statusError{status: status, body: body}
}

func exchange(t *testing.T, addr, raw string) (string, string) {
	t.Helper()
	status, body, err := exchangeErr(addr, raw)
	if err != nil {
		t.Fatal(err)
	}
	return status, body
}

func exchangeErr(addr, raw string) (string, string, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return "", "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(raw)); err != nil {
		return "", "", err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		return "", "", err
	}
	var contentLength int
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length: ")))
			if err != nil {
				return "", "", err
			}
		}
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(br, buf); err != nil {
		return "", "", err
	}
	return status, string(buf), nil
}
