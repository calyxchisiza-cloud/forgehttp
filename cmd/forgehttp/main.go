package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"forgehttp/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "TCP address to listen on")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &server.Server{Addr: *addr}
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}
