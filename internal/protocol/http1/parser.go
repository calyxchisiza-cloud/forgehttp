package http1

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

var (
	errLineTooLarge = errors.New("line exceeds limit")
	errBadCRLF      = errors.New("line must end with CRLF")
)

// ReadRequest parses one HTTP/1 request from r.
// TCP is a byte stream, so r may deliver the request in fragments.
// A transfer-encoding is rejected and its body is left in r.
func ReadRequest(r *bufio.Reader, limits Limits) (*Request, error) {
	limits = limits.normalized()

	line, _, err := readCRLFLine(r, limits.MaxRequestLine)
	if err != nil {
		return nil, requestLineError(err)
	}
	method, target, proto, err := parseRequestLine(line)
	if err != nil {
		return nil, err
	}
	path, rawQuery, err := splitTarget(target)
	if err != nil {
		return nil, err
	}

	header := make(Header)
	used := 0
	for {
		remain := limits.MaxHeaders - used
		if remain <= 0 {
			return nil, ErrHeadersTooLarge
		}
		field, n, err := readCRLFLine(r, remain)
		if err != nil {
			if errors.Is(err, errLineTooLarge) {
				return nil, ErrHeadersTooLarge
			}
			if errors.Is(err, errBadCRLF) {
				return nil, ErrBadHeader
			}
			return nil, err
		}
		used += n
		if field == "" {
			break
		}
		name, value, err := parseHeader(field)
		if err != nil {
			return nil, err
		}
		header.add(name, value)
	}

	req := &Request{
		Method:        method,
		Target:        target,
		Path:          path,
		RawQuery:      rawQuery,
		Proto:         proto,
		Header:        header,
		ContentLength: -1,
	}
	if err := validateHost(req); err != nil {
		return nil, err
	}
	if err := readBody(r, req, limits.MaxBody); err != nil {
		return nil, err
	}
	return req, nil
}

func requestLineError(err error) error {
	switch {
	case errors.Is(err, errLineTooLarge):
		return ErrRequestLineTooLarge
	case errors.Is(err, errBadCRLF):
		return ErrBadRequestLine
	default:
		return err
	}
}

func parseRequestLine(line string) (method, target, proto string, err error) {
	method, rest, ok := strings.Cut(line, " ")
	if !ok {
		return "", "", "", ErrBadRequestLine
	}
	target, proto, ok = strings.Cut(rest, " ")
	if !ok || strings.Contains(proto, " ") || method == "" || target == "" || proto == "" {
		return "", "", "", ErrBadRequestLine
	}
	if !isToken(method) {
		return "", "", "", ErrBadRequestLine
	}
	switch proto {
	case "HTTP/1.0", "HTTP/1.1":
	default:
		return "", "", "", ErrBadVersion
	}
	return method, target, proto, nil
}

func splitTarget(target string) (path, rawQuery string, err error) {
	if target == "*" {
		return "*", "", nil
	}
	if target[0] != '/' {
		return "", "", ErrBadRequestTarget
	}
	for i := 0; i < len(target); i++ {
		c := target[i]
		if c < 0x21 || c == 0x7F || c == '#' {
			return "", "", ErrBadRequestTarget
		}
	}
	path, rawQuery, _ = strings.Cut(target, "?")
	if path == "" {
		return "", "", ErrBadRequestTarget
	}
	return path, rawQuery, nil
}

func parseHeader(line string) (name, value string, err error) {
	if line[0] == ' ' || line[0] == '\t' {
		return "", "", ErrBadHeader
	}
	name, value, ok := strings.Cut(line, ":")
	if !ok || name == "" || strings.TrimSpace(name) != name || !isToken(name) {
		return "", "", ErrBadHeader
	}
	value = trimOWS(value)
	if !validHeaderValue(value) {
		return "", "", ErrBadHeader
	}
	return name, value, nil
}

func validateHost(req *Request) error {
	hosts := req.Header.Values("Host")
	if len(hosts) > 1 {
		return ErrMissingHost
	}
	if req.Proto == "HTTP/1.1" && (len(hosts) != 1 || hosts[0] == "") {
		return ErrMissingHost
	}
	return nil
}

func readBody(r *bufio.Reader, req *Request, maxBody int) error {
	if len(req.Header.Values("Transfer-Encoding")) > 0 {
		if len(req.Header.Values("Content-Length")) > 0 {
			return ErrAmbiguousFraming
		}
		return ErrUnsupportedTransferEncoding
	}
	lengths := req.Header.Values("Content-Length")
	if len(lengths) == 0 {
		return nil
	}
	if len(lengths) != 1 {
		return ErrAmbiguousFraming
	}
	n, err := parseContentLength(lengths[0])
	if err != nil {
		return err
	}
	if n > int64(maxBody) {
		return ErrBodyTooLarge
	}
	req.ContentLength = n
	if n == 0 {
		req.Body = []byte{}
		return nil
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	req.Body = body
	return nil
}

func parseContentLength(v string) (int64, error) {
	if v == "" {
		return 0, ErrBadHeader
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, ErrAmbiguousFraming
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, ErrBodyTooLarge
	}
	return n, nil
}

// readCRLFLine reads one CRLF-terminated line of at most max bytes, including the CRLF.
// The returned string does not include the CRLF. The count includes it.
// A line with no newline inside max bytes fails without reading another byte.
func readCRLFLine(r *bufio.Reader, max int) (string, int, error) {
	buf := make([]byte, 0, min(max, 128))
	for len(buf) < max {
		b, err := r.ReadByte()
		if err != nil {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return "", len(buf), io.ErrUnexpectedEOF
			}
			return "", 0, err
		}
		buf = append(buf, b)
		if b != '\n' {
			continue
		}
		if len(buf) < 2 || buf[len(buf)-2] != '\r' {
			return "", len(buf), errBadCRLF
		}
		return string(buf[:len(buf)-2]), len(buf), nil
	}
	return "", 0, errLineTooLarge
}

func trimOWS(s string) string {
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func validHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\t' && (c < 0x20 || c == 0x7F) {
			return false
		}
	}
	return true
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTChar(s[i]) {
			return false
		}
	}
	return true
}

func isTChar(c byte) bool {
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
