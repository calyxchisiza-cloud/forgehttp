package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"forgehttp/internal/protocol/http1"
	"forgehttp/internal/server"
	"forgehttp/internal/transport"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "TCP address to listen on")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := transport.Listen(*addr)
	if err != nil {
		log.Fatal(err)
	}
	bound := ln.Addr().String()
	srv := &server.Server{
		ReadTimeout: time.Second,
		SkipTrace: func(method, target string) bool {
			return method == "GET" && target == "/console/state"
		},
	}
	if err := register(srv, bound); err != nil {
		log.Fatal(err)
	}
	log.Printf("console on http://%s/", bound)
	if err := srv.Serve(ctx, ln); err != nil {
		log.Fatal(err)
	}
}

func register(srv *server.Server, addr string) error {
	if err := srv.Handle("GET", "/", func(*http1.Request) *http1.Response {
		return &http1.Response{
			Status: 200,
			Header: http1.Header{"Content-Type": {"text/html; charset=utf-8"}},
			Body:   []byte(consolePage),
		}
	}); err != nil {
		return err
	}
	if err := srv.Handle("GET", "/console/state", func(*http1.Request) *http1.Response {
		return http1.JSONData(200, srv.Snapshot())
	}); err != nil {
		return err
	}
	if err := srv.Handle("POST", "/console/try", tryHandler(addr)); err != nil {
		return err
	}
	return registerUsers(srv, newUserStore())
}
