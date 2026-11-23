package http1

import (
	"bytes"
	"fmt"
	"io"
	"slices"
)

// Write serializes the response to w.
// Content-Length is the length of Body.
// Persist sends Connection: keep-alive. Otherwise Connection is close.
// A caller-supplied Content-Length or Connection field is not sent.
// Transfer-Encoding is rejected. Header names and values are checked so a value cannot inject another header.
func (r *Response) Write(w io.Writer) error {
	if r.Status < 100 || r.Status > 599 {
		return ErrBadStatus
	}
	names := make([]string, 0, len(r.Header))
	for name, values := range r.Header {
		if CanonicalHeader(name) != name || !isToken(name) {
			return ErrBadResponseHeader
		}
		if name == "Transfer-Encoding" {
			return ErrBadResponseHeader
		}
		for _, value := range values {
			if !validHeaderValue(value) {
				return ErrBadResponseHeader
			}
		}
		switch name {
		case "Content-Length", "Connection":
			continue
		default:
			names = append(names, name)
		}
	}
	slices.Sort(names)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", r.Status, reasonPhrase(r.Status))
	for _, name := range names {
		for _, value := range r.Header[name] {
			fmt.Fprintf(&buf, "%s: %s\r\n", name, value)
		}
	}
	connection := "close"
	if r.Persist {
		connection = "keep-alive"
	}
	fmt.Fprintf(&buf, "Connection: %s\r\nContent-Length: %d\r\n\r\n", connection, len(r.Body))
	buf.Write(r.Body)
	return writeAll(w, buf.Bytes())
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
