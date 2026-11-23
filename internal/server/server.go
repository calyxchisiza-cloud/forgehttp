// Package server accepts TCP connections one at a time and shuts them down
// when its context is cancelled.
package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"sync"
	"time"

	"forgehttp/internal/protocol/http1"
	"forgehttp/internal/transport"
)

// Server is a sequential TCP accept loop.
// A connected client is served to completion before Accept is called again.
type Server struct {
	// Addr is the host:port to bind. Empty means 127.0.0.1:8080.
	Addr string

	// ReadTimeout bounds how long the request may take to arrive.
	// Zero uses 30 seconds.
	ReadTimeout time.Duration

	// WriteTimeout bounds how long the parse summary may take to leave.
	// Zero uses 10 seconds.
	WriteTimeout time.Duration

	// Limits bound the request line, headers, and body. Zero values use protocol defaults.
	Limits http1.Limits

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
	if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout())); err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
		return
	}
	req, err := http1.ReadRequest(bufio.NewReader(conn), s.Limits)
	if err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
		return
	}
	s.logger().Printf("%s %s %s", req.Method, req.Target, conn.RemoteAddr())
	if err := conn.SetWriteDeadline(time.Now().Add(s.writeTimeout())); err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
		return
	}
	if err := writeParseSummary(conn, req); err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
	}
}

func (s *Server) readTimeout() time.Duration {
	if s.ReadTimeout > 0 {
		return s.ReadTimeout
	}
	return 30 * time.Second
}

func (s *Server) writeTimeout() time.Duration {
	if s.WriteTimeout > 0 {
		return s.WriteTimeout
	}
	return 10 * time.Second
}

// writeParseSummary sends the parsed request as plain text.
// It is a debug dump so a client can finish the exchange. The response writer is a later phase.
func writeParseSummary(w io.Writer, req *http1.Request) error {
	var payload bytes.Buffer
	fmt.Fprintf(&payload, "%s %s %s\n", req.Method, req.Target, req.Proto)
	names := make([]string, 0, len(req.Header))
	for name := range req.Header {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		for _, value := range req.Header[name] {
			fmt.Fprintf(&payload, "%s: %s\n", name, value)
		}
	}
	if len(req.Body) > 0 {
		payload.WriteByte('\n')
		payload.Write(req.Body)
	}
	var head bytes.Buffer
	fmt.Fprintf(&head, "HTTP/1.1 200 OK\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", payload.Len())
	if err := writeAll(w, head.Bytes()); err != nil {
		return err
	}
	return writeAll(w, payload.Bytes())
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
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
