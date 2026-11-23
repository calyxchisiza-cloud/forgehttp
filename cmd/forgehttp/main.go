package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"forgehttp/internal/protocol/http1"
	"forgehttp/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "TCP address to listen on")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &server.Server{Addr: *addr}
	hello := func(*http1.Request) *http1.Response {
		return http1.JSON(200, []byte(http1.HelloMessage))
	}
	if err := srv.Handle("GET", "/", hello); err != nil {
		log.Fatal(err)
	}
	if err := srv.Handle("GET", "/hello", hello); err != nil {
		log.Fatal(err)
	}
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}
