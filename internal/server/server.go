// Package server accepts TCP connections one at a time and shuts them down
// when its context is cancelled.
package server

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"forgehttp/internal/transport"
)

// Server is a sequential TCP accept loop.
// A connected client is served to completion before Accept is called again.
type Server struct {
	// Addr is the host:port to bind. Empty means 127.0.0.1:8080.
	Addr string

	// Limits are passed to each connection exchange. Zero values use transport defaults.
	Limits transport.Limits

	// Logger receives accept errors and per-connection failures.
	// Nil uses the standard logger.
	Logger *log.Logger

	// onAccepted, when set, runs after a connection is tracked and before the
	// line exchange. Tests use it to observe the sequential loop.
	onAccepted func(net.Conn)

	mu      sync.Mutex
	closing bool
	active  net.Conn
}

// ListenAndServe binds Addr and serves until ctx is cancelled or Accept fails.
// A cancelled context closes the listener and returns nil.
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := s.Addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	ln, err := transport.Listen(addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve accepts from ln until ctx is cancelled or Accept fails.
// Serve closes ln before it returns.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	// Cancelling this child context on return releases the shutdown goroutine
	// even when Serve stops for a reason other than the caller's cancel.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		s.beginShutdown(ln)
	}()

	s.logger().Printf("listening on %s", ln.Addr())

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.isClosing() || ctx.Err() != nil {
				return nil
			}
			if isTemporary(err) {
				// A persistent temporary failure must not spin the accept loop.
				s.logger().Printf("accept: %v", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return err
		}
		s.serve(conn)
	}
}

func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	if !s.track(conn) {
		return
	}
	defer s.untrack(conn)

	s.logger().Printf("accepted %s", conn.RemoteAddr())
	if s.onAccepted != nil {
		s.onAccepted(conn)
	}
	if err := transport.Exchange(conn, s.Limits); err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
	}
}

func (s *Server) beginShutdown(ln net.Listener) {
	s.mu.Lock()
	s.closing = true
	conn := s.active
	s.mu.Unlock()

	// Accept and a blocked Read both have to wake up. Closing the listener
	// unblocks Accept. Closing the active conn unblocks Read.
	_ = ln.Close()
	if conn != nil {
		_ = conn.Close()
	}
}

func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.active = conn
	return true
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == conn {
		s.active = nil
	}
}

func (s *Server) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

func (s *Server) logger() *log.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return log.Default()
}

func isTemporary(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Temporary()
}
