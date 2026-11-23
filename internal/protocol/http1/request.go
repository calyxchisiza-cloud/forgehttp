// Package http1 reads and writes HTTP/1 messages.
// It does not use net/http.
package http1

import (
	"context"
	"errors"
)

const (
	// DefaultMaxRequestLine is the most bytes in the request line, including CRLF.
	DefaultMaxRequestLine = 8 << 10

	// DefaultMaxHeaders is the most bytes in the header block, including the blank line.
	DefaultMaxHeaders = 32 << 10

	// DefaultMaxBody is the most bytes read from a Content-Length body.
	DefaultMaxBody = 1 << 20
)

// Limits bounds one request. Zero values use the defaults above.
type Limits struct {
	MaxRequestLine int
	MaxHeaders     int
	MaxBody        int
}

func (l Limits) normalized() Limits {
	if l.MaxRequestLine <= 0 {
		l.MaxRequestLine = DefaultMaxRequestLine
	}
	if l.MaxHeaders <= 0 {
		l.MaxHeaders = DefaultMaxHeaders
	}
	if l.MaxBody <= 0 {
		l.MaxBody = DefaultMaxBody
	}
	return l
}

// Header is a set of header fields keyed by canonical name.
// A repeated field is kept as more than one value.
type Header map[string][]string

// Get returns the first value for name, or an empty string.
func (h Header) Get(name string) string {
	if h == nil {
		return ""
	}
	vs := h[CanonicalHeader(name)]
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

// Values returns every value for name. The caller must not modify the slice.
func (h Header) Values(name string) []string {
	if h == nil {
		return nil
	}
	return h[CanonicalHeader(name)]
}

func (h Header) add(name, value string) {
	key := CanonicalHeader(name)
	h[key] = append(h[key], value)
}

// Request is one parsed HTTP/1 request.
// ContentLength is -1 when the request has no Content-Length field.
type Request struct {
	Method        string
	Target        string
	Path          string
	RawQuery      string
	Proto         string
	Header        Header
	Body          []byte
	ContentLength int64
	ctx           context.Context
}

// Context returns the request context. It is never nil.
func (r *Request) Context() context.Context {
	if r == nil || r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

// SetContext replaces the request context. A nil context clears it to the background context.
func (r *Request) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.ctx = ctx
}

var (
	// ErrRequestLineTooLarge means the request line had no CRLF within the limit.
	ErrRequestLineTooLarge = errors.New("request line exceeds maximum size")

	// ErrHeadersTooLarge means the header block exceeded its limit.
	ErrHeadersTooLarge = errors.New("headers exceed maximum size")

	// ErrBodyTooLarge means Content-Length is above the body limit.
	ErrBodyTooLarge = errors.New("body exceeds maximum size")

	// ErrBadRequestLine means the request line is not METHOD SP target SP version.
	ErrBadRequestLine = errors.New("malformed request line")

	// ErrBadRequestTarget means the target is not an origin-form path or "*".
	ErrBadRequestTarget = errors.New("malformed request target")

	// ErrBadVersion means the version is not HTTP/1.0 or HTTP/1.1.
	ErrBadVersion = errors.New("unsupported HTTP version")

	// ErrBadHeader means a header line, name, or value is not valid.
	ErrBadHeader = errors.New("malformed header")

	// ErrMissingHost means an HTTP/1.1 request did not contain exactly one Host.
	ErrMissingHost = errors.New("HTTP/1.1 request requires one Host header")

	// ErrAmbiguousFraming means Content-Length and Transfer-Encoding disagree,
	// or Content-Length appears more than once.
	ErrAmbiguousFraming = errors.New("ambiguous message framing")

	// ErrUnsupportedTransferEncoding means the request uses a transfer coding.
	// The body is left unread. Chunked bytes must not be treated as Content-Length.
	ErrUnsupportedTransferEncoding = errors.New("unsupported transfer encoding")
)

// CanonicalHeader returns the canonical form of a header name: "content-length" becomes "Content-Length".
func CanonicalHeader(name string) string {
	b := []byte(name)
	upper := true
	for i, c := range b {
		switch {
		case upper && c >= 'a' && c <= 'z':
			b[i] = c - 'a' + 'A'
		case !upper && c >= 'A' && c <= 'Z':
			b[i] = c - 'A' + 'a'
		}
		upper = c == '-'
	}
	return string(b)
}
