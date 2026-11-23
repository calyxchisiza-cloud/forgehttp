// Package transport is the TCP boundary for ForgeHTTP.
// It listens for connections and exchanges one raw line per connection.
package transport

import (
	"bufio"
	"errors"
	"io"
	"net"
	"time"
)

const (
	// DefaultMaxMessage is the most bytes read while looking for a newline,
	// including the newline. A line with no newline inside this many bytes
	// is rejected and the rest of the stream is left unread.
	DefaultMaxMessage = 8 << 10

	// DefaultReadTimeout bounds how long a connection may sit before its
	// line arrives. The accept loop is sequential, so this is what stops a
	// silent client from pinning the process.
	DefaultReadTimeout = 30 * time.Second

	// DefaultWriteTimeout bounds how long the reply may take to leave.
	DefaultWriteTimeout = 10 * time.Second
)

// ErrMessageTooLarge is returned when a line has no newline within MaxMessage bytes.
var ErrMessageTooLarge = errors.New("message exceeds maximum size")

// errMessageCap is the internal signal that readLine has taken MaxMessage bytes
// and still has no newline. It stays inside this package so callers see ErrMessageTooLarge.
var errMessageCap = errors.New("message cap")

// Limits configures one connection exchange.
// Zero values are replaced with the defaults above.
type Limits struct {
	MaxMessage   int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func (l Limits) normalized() Limits {
	if l.MaxMessage <= 0 {
		l.MaxMessage = DefaultMaxMessage
	}
	if l.ReadTimeout <= 0 {
		l.ReadTimeout = DefaultReadTimeout
	}
	if l.WriteTimeout <= 0 {
		l.WriteTimeout = DefaultWriteTimeout
	}
	return l
}

// Listen opens a TCP listener. addr is a host:port string such as 127.0.0.1:8080.
func Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// Exchange reads one newline-terminated message and writes one newline-terminated reply.
// "Hello Server" is answered with "Hello Client". Any other line is answered with "Echo: <line>".
// Exchange does not close conn.
func Exchange(conn net.Conn, limits Limits) error {
	limits = limits.normalized()
	if err := conn.SetReadDeadline(time.Now().Add(limits.ReadTimeout)); err != nil {
		return err
	}
	line, err := readLine(conn, limits.MaxMessage)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(limits.WriteTimeout)); err != nil {
		return err
	}
	return writeAll(conn, []byte(replyFor(line)))
}

func replyFor(line string) string {
	if line == "Hello Server" {
		return "Hello Client\n"
	}
	return "Echo: " + line + "\n"
}

// readLine collects a byte stream until '\n' or max bytes.
// TCP does not deliver messages: one Read may be a fragment, so this loops.
// Bytes after max are left unread.
func readLine(r io.Reader, max int) (string, error) {
	br := bufio.NewReaderSize(&capReader{r: r, n: max}, max)
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		// ReadSlice reuses its buffer on the next call. Copy before continuing.
		n := len(buf) + len(frag)
		if errors.Is(err, errMessageCap) || n > max || (err == bufio.ErrBufferFull && n >= max) {
			return "", ErrMessageTooLarge
		}
		buf = append(buf, frag...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return "", io.ErrUnexpectedEOF
			}
			return "", err
		}
		break
	}
	buf = buf[:len(buf)-1]
	if len(buf) > 0 && buf[len(buf)-1] == '\r' {
		buf = buf[:len(buf)-1]
	}
	return string(buf), nil
}

// capReader stops after n bytes so a bufio buffer larger than the message
// limit cannot read past it. bufio raises its buffer to at least 16 bytes.
type capReader struct {
	r io.Reader
	n int
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, errMessageCap
	}
	if len(p) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= n
	return n, err
}

// writeAll keeps writing until p is exhausted. A short Write must not drop the tail.
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
