package main

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"forgehttp/internal/server"
	"forgehttp/internal/transport"
)

func TestConsoleScenarios(t *testing.T) {
	srv := &server.Server{
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 2 * time.Second,
		IdleTimeout:  2 * time.Second,
		Logger:       log.New(io.Discard, "", 0),
		SkipTrace: func(method, target string) bool {
			return method == "GET" && target == "/console/state"
		},
	}
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := register(srv, addr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	}()

	page, err := runTry(addr, tryRequest{Method: "GET", Target: "/", Proto: "HTTP/1.1", Headers: []headerField{{Name: "Host", Value: "localhost"}}})
	if err != nil || len(page.Exchanges) != 1 || !strings.Contains(page.Exchanges[0].Body, "<h1>ForgeHTTP</h1>") {
		t.Fatalf("page err %v body %q", err, exchangeBody(page))
	}

	once, err := runTry(addr, tryRequest{Mode: "once", Method: "GET", Target: "/api/users", Headers: []headerField{{Name: "Host", Value: "localhost"}}})
	if err != nil || len(once.Exchanges) != 1 || once.Exchanges[0].Body != "[]" || !strings.Contains(once.Exchanges[0].RawResponse, "Connection: keep-alive") {
		t.Fatalf("once err %v %+v", err, once)
	}

	twice, err := runTry(addr, tryRequest{Mode: "twice", Method: "GET", Target: "/api/users", Headers: []headerField{{Name: "Host", Value: "localhost"}}})
	if err != nil || len(twice.Exchanges) != 2 || twice.Exchanges[1].Body != "[]" {
		t.Fatalf("twice err %v count %d", err, len(twice.Exchanges))
	}

	old, err := runTry(addr, tryRequest{Mode: "http10", Method: "GET", Target: "/api/users"})
	if err != nil || len(old.Exchanges) != 1 || !strings.Contains(old.Exchanges[0].RawResponse, "Connection: close") {
		t.Fatalf("http10 err %v %+v", err, old)
	}

	stall, err := runTry(addr, tryRequest{Mode: "stall", Method: "GET", Target: "/api/users", Proto: "HTTP/1.1"})
	if err != nil || len(stall.Exchanges) != 1 || !strings.Contains(stall.Exchanges[0].Status, "408") || stall.Exchanges[0].Body != "request timeout" {
		t.Fatalf("stall err %v %+v", err, stall)
	}
}

func exchangeBody(result tryResult) string {
	if len(result.Exchanges) == 0 {
		return ""
	}
	return result.Exchanges[0].Body
}
