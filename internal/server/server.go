// Package server accepts TCP connections and serves each one on its own
// goroutine. A connection can carry more than one request, in order.
// Cancelling the context stops Accept, finishes a response already started,
// and closes connections that are still waiting for a request.
package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"forgehttp/internal/protocol/http1"
	"forgehttp/internal/router"
	"forgehttp/internal/transport"
)

// defaultMaxConns is the connection limit when MaxConns is zero.
const defaultMaxConns = 100

// defaultIdleTimeout is how long a connection may wait for the next request.
const defaultIdleTimeout = 30 * time.Second

// connStage is what a connection is doing. Shutdown closes a connection that
// is still reading. A connection that is already handling a request is left
// open until that response is written.
type connStage int

const (
	stageReading connStage = iota
	stageServing
)

// Server accepts connections and serves each one on its own goroutine.
// The goroutine reads a request, writes the response, then reads the next
// request on that connection. It does not write the next response first.
type Server struct {
	// Addr is the host:port to bind. Empty means 127.0.0.1:8080.
	Addr string

	// ReadTimeout bounds how long the request may take to arrive.
	// Zero uses 30 seconds.
	ReadTimeout time.Duration

	// WriteTimeout bounds how long the response may take to leave.
	// Zero uses 10 seconds.
	WriteTimeout time.Duration

	// IdleTimeout bounds how long a connection may wait for the first byte
	// of the next request. Zero uses 30 seconds. Once a byte arrives,
	// ReadTimeout bounds the rest of that request.
	IdleTimeout time.Duration

	// MaxConns is how many connections may be served at once.
	// Zero uses 100. At the limit, Accept waits and further clients stay in
	// the kernel listen queue.
	MaxConns int

	// Limits bound the request line, headers, and body. Zero values use protocol defaults.
	Limits http1.Limits

	// Logger receives accept errors and per-connection failures.
	// Nil uses the standard logger.
	Logger *log.Logger

	// onAccepted, when set, runs after a connection is tracked and before the
	// request is read. Tests use it to observe that a connection was accepted.
	onAccepted func(net.Conn)

	// SkipTrace, when it returns true, omits that request from Snapshot and
	// from the connection's request count. The request is still served.
	SkipTrace func(method, target string) bool

	routes *router.Router

	mu      sync.Mutex
	closing bool
	conns   map[net.Conn]*connState
	traces  []Trace
	slots   chan struct{}
	wg      sync.WaitGroup
}

// connState is one accepted connection.
type connState struct {
	stage    connStage
	remote   string
	requests int
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
// Each accepted connection is served on its own goroutine.
// Serve closes ln and waits for those goroutines before it returns.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.isClosing() {
		_ = ln.Close()
		return nil
	}

	// Cancelling this child context on return releases the shutdown goroutine
	// even when Serve stops for a reason other than the caller's cancel.
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		// Cancel first so beginShutdown closes the listener and every connection.
		// Wait after that, or a handler still blocked in Read would stall Serve.
		cancel()
		s.wg.Wait()
	}()

	s.mu.Lock()
	s.conns = make(map[net.Conn]*connState)
	s.slots = make(chan struct{}, s.maxConns())
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.beginShutdown(ln)
	}()

	s.logger().Printf("listening on %s", ln.Addr())

	for {
		// Taking a slot before Accept stops the loop at MaxConns. The listen
		// queue then holds new clients until a connection goroutine finishes.
		if !s.takeSlot(ctx) {
			return nil
		}
		conn, err := ln.Accept()
		if err != nil {
			s.releaseSlot()
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
		if !s.track(conn) {
			s.releaseSlot()
			_ = conn.Close()
			return nil
		}
		s.wg.Add(1)
		go s.serve(ctx, conn)
	}
}

// Handle registers an exact method and path. Register routes before serving traffic.
func (s *Server) Handle(method, path string, h router.Handler) error {
	return s.router().Handle(method, path, h)
}

func (s *Server) serve(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()
	defer s.releaseSlot()
	defer conn.Close()
	defer s.untrack(conn)

	s.logger().Printf("accepted %s", conn.RemoteAddr())
	if s.onAccepted != nil {
		s.onAccepted(conn)
	}

	reader := bufio.NewReader(conn)
	for {
		if s.isClosing() {
			return
		}
		// Idle wait: no bytes yet. A timeout or a client close ends the connection
		// without a response. Shutdown closes the socket, which unblocks Peek.
		if err := conn.SetReadDeadline(time.Now().Add(s.idleTimeout())); err != nil {
			s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
			return
		}
		if _, err := reader.Peek(1); err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
			}
			return
		}
		if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout())); err != nil {
			s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
			return
		}
		req, err := http1.ReadRequest(reader, s.Limits)
		if err != nil {
			s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
			if errors.Is(err, io.EOF) {
				return
			}
			resp := errorResponse(err)
			if isTimeout(err) {
				resp = http1.Text(408, "request timeout")
			}
			_ = s.writeResponse(conn, resp)
			s.remember(Trace{
				Remote:     conn.RemoteAddr().String(),
				Status:     resp.Status,
				Connection: "close",
				Note:       err.Error(),
			})
			return
		}

		// The request is complete. Shutdown must not close this socket until
		// the response below has been written.
		quiet := s.skips(req.Method, req.Target)
		s.markStage(conn, stageServing, !quiet)
		s.logger().Printf("%s %s %s", req.Method, req.Target, conn.RemoteAddr())
		req.SetContext(ctx)
		resp := s.router().Serve(req)
		if resp == nil {
			resp = http1.Text(500, "nil response")
		}
		resp.Persist = keepAlive(req) && ctx.Err() == nil && !s.isClosing()
		token := "close"
		if resp.Persist {
			token = "keep-alive"
		}
		if err := s.writeResponse(conn, resp); err != nil {
			if !quiet {
				s.remember(Trace{
					Remote: conn.RemoteAddr().String(),
					Method: req.Method,
					Target: req.Target,
					Proto:  req.Proto,
					Status: resp.Status,
					Note:   err.Error(),
				})
			}
			return
		}
		if !quiet {
			s.remember(Trace{
				Remote:     conn.RemoteAddr().String(),
				Method:     req.Method,
				Target:     req.Target,
				Proto:      req.Proto,
				Status:     resp.Status,
				Connection: token,
			})
		}
		if !resp.Persist {
			return
		}
		s.markStage(conn, stageReading, false)
	}
}

func (s *Server) writeResponse(conn net.Conn, resp *http1.Response) error {
	if err := conn.SetWriteDeadline(time.Now().Add(s.writeTimeout())); err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
		return err
	}
	if err := resp.Write(conn); err != nil {
		s.logger().Printf("%s: %v", conn.RemoteAddr(), err)
		return err
	}
	return nil
}

func errorResponse(err error) *http1.Response {
	switch {
	case errors.Is(err, http1.ErrRequestLineTooLarge), errors.Is(err, http1.ErrHeadersTooLarge):
		return http1.Text(431, "headers too large")
	case errors.Is(err, http1.ErrBodyTooLarge):
		return http1.Text(413, "body too large")
	case errors.Is(err, http1.ErrBadVersion):
		return http1.Text(505, "unsupported HTTP version")
	case errors.Is(err, http1.ErrUnsupportedTransferEncoding):
		return http1.Text(501, "transfer encoding not implemented")
	case errors.Is(err, http1.ErrAmbiguousFraming):
		return http1.Text(400, "ambiguous message framing")
	default:
		return http1.Text(400, "bad request")
	}
}

func (s *Server) router() *router.Router {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes == nil {
		s.routes = router.New()
	}
	return s.routes
}

func (s *Server) maxConns() int {
	if s.MaxConns > 0 {
		return s.MaxConns
	}
	return defaultMaxConns
}

func (s *Server) takeSlot(ctx context.Context) bool {
	select {
	case s.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) releaseSlot() {
	<-s.slots
}

func (s *Server) idleTimeout() time.Duration {
	if s.IdleTimeout > 0 {
		return s.IdleTimeout
	}
	return defaultIdleTimeout
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

func (s *Server) beginShutdown(ln net.Listener) {
	s.mu.Lock()
	s.closing = true
	var waiting []net.Conn
	for conn, st := range s.conns {
		if st.stage == stageReading {
			waiting = append(waiting, conn)
		}
	}
	s.mu.Unlock()

	// Accept and a blocked idle read both have to wake up. Closing the listener
	// unblocks Accept. Closing a connection that is still waiting for a request
	// unblocks its read. A connection already handling a request stays open
	// until that response is written.
	_ = ln.Close()
	for _, conn := range waiting {
		_ = conn.Close()
	}
}

func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	remote := ""
	if conn.RemoteAddr() != nil {
		remote = conn.RemoteAddr().String()
	}
	s.conns[conn] = &connState{stage: stageReading, remote: remote}
	return true
}

func (s *Server) markStage(conn net.Conn, stage connStage, count bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.conns[conn]
	if st == nil {
		return
	}
	st.stage = stage
	if count {
		st.requests++
	}
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

func (s *Server) skips(method, target string) bool {
	if s.SkipTrace == nil {
		return false
	}
	return s.SkipTrace(method, target)
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

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// keepAlive reports whether the connection can carry another request.
// HTTP/1.1 stays open unless the client sent Connection: close.
// HTTP/1.0 stays open only when the client sent Connection: keep-alive.
// close wins when both tokens are present.
func keepAlive(req *http1.Request) bool {
	if req == nil {
		return false
	}
	var closeTok, keepTok bool
	for _, value := range req.Header.Values("Connection") {
		for _, part := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(part)) {
			case "close":
				closeTok = true
			case "keep-alive":
				keepTok = true
			}
		}
	}
	if closeTok {
		return false
	}
	if req.Proto == "HTTP/1.0" {
		return keepTok
	}
	return req.Proto == "HTTP/1.1"
}
