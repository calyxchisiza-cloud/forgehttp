package http1

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadRequestGET(t *testing.T) {
	raw := "" +
		"GET /hello?x=1 HTTP/1.1\r\n" +
		"Host: localhost:8080\r\n" +
		"User-Agent: ForgeHTTP-Test\r\n" +
		"Accept: application/json\r\n" +
		"\r\n"
	req := readRaw(t, raw, Limits{})
	if req.Method != "GET" || req.Path != "/hello" || req.RawQuery != "x=1" || req.Proto != "HTTP/1.1" {
		t.Fatalf("request = %+v", req)
	}
	if req.Target != "/hello?x=1" {
		t.Fatalf("target %q", req.Target)
	}
	if req.Header.Get("host") != "localhost:8080" {
		t.Fatalf("Host %q", req.Header.Get("Host"))
	}
	if req.Header.Get("User-Agent") != "ForgeHTTP-Test" || req.Header.Get("Accept") != "application/json" {
		t.Fatalf("headers %#v", req.Header)
	}
	if req.ContentLength != -1 || len(req.Body) != 0 {
		t.Fatalf("unexpected body %d %q", req.ContentLength, req.Body)
	}
}

func TestReadRequestFragmented(t *testing.T) {
	raw := "GET /hello HTTP/1.1\r\nHost: localhost\r\n\r\n"
	req := readFrom(t, oneByteReader{r: strings.NewReader(raw)}, Limits{})
	if req.Method != "GET" || req.Path != "/hello" || req.Header.Get("Host") != "localhost" {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadRequestPostBody(t *testing.T) {
	raw := "POST /api/users HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\n\r\nhello"
	req := readRaw(t, raw, Limits{})
	if req.ContentLength != 5 || string(req.Body) != "hello" {
		t.Fatalf("body %d %q", req.ContentLength, req.Body)
	}
}

func TestReadRequestBodySplitFromHeaders(t *testing.T) {
	head := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\n\r\n"
	body := "abcd"
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		pr.Close()
		pw.Close()
	})
	go func() {
		_, _ = pw.Write([]byte(head))
		_, _ = pw.Write([]byte(body))
		_ = pw.Close()
	}()
	req := readFrom(t, pr, Limits{})
	if string(req.Body) != "abcd" {
		t.Fatalf("body %q", req.Body)
	}
}

func TestReadRequestRejectsChunkedAndLeavesBody(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"
	br := bufio.NewReader(strings.NewReader(raw))
	_, err := ReadRequest(br, Limits{})
	if !errors.Is(err, ErrUnsupportedTransferEncoding) {
		t.Fatalf("error %v", err)
	}
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "5\r\nhello\r\n0\r\n\r\n" {
		t.Fatalf("body was consumed: %q", rest)
	}
}

func TestReadRequestRejectsMixedFraming(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\nhello"
	br := bufio.NewReader(strings.NewReader(raw))
	_, err := ReadRequest(br, Limits{})
	if !errors.Is(err, ErrAmbiguousFraming) {
		t.Fatalf("error %v", err)
	}
	rest, _ := io.ReadAll(br)
	if string(rest) != "hello" {
		t.Fatalf("body was consumed: %q", rest)
	}
}

func TestReadRequestRejectsRepeatedContentLength(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 1\r\n\r\n"
	_, err := ReadRequest(bufio.NewReader(strings.NewReader(raw)), Limits{})
	if !errors.Is(err, ErrAmbiguousFraming) {
		t.Fatalf("error %v", err)
	}
}

func TestReadRequestRequiresHost(t *testing.T) {
	raw := "GET / HTTP/1.1\r\n\r\n"
	_, err := ReadRequest(bufio.NewReader(strings.NewReader(raw)), Limits{})
	if !errors.Is(err, ErrMissingHost) {
		t.Fatalf("error %v", err)
	}
}

func TestReadRequestHTTP10WithoutHost(t *testing.T) {
	raw := "GET / HTTP/1.0\r\n\r\n"
	req := readRaw(t, raw, Limits{})
	if req.Proto != "HTTP/1.0" || req.Header.Get("Host") != "" {
		t.Fatalf("request = %+v", req)
	}
}

func TestReadRequestRejectsBadVersionAndLineEndings(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{name: "http2", raw: "GET / HTTP/2.0\r\nHost: localhost\r\n\r\n", want: ErrBadVersion},
		{name: "lf only", raw: "GET / HTTP/1.1\nHost: localhost\r\n\r\n", want: ErrBadRequestLine},
		{name: "no colon", raw: "GET / HTTP/1.1\r\nHost localhost\r\n\r\n", want: ErrBadHeader},
		{name: "space before colon", raw: "GET / HTTP/1.1\r\nHost : localhost\r\n\r\n", want: ErrBadHeader},
		{name: "absolute form", raw: "GET http://example/ HTTP/1.1\r\nHost: example\r\n\r\n", want: ErrBadRequestTarget},
		{name: "ctl in value", raw: "GET / HTTP/1.1\r\nHost: local\x00host\r\n\r\n", want: ErrBadHeader},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadRequest(bufio.NewReader(strings.NewReader(tc.raw)), Limits{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadRequestLimits(t *testing.T) {
	limits := Limits{MaxRequestLine: 16, MaxHeaders: 32, MaxBody: 4}
	longLine := "GET " + strings.Repeat("/a", 20) + " HTTP/1.1\r\n"
	_, err := ReadRequest(bufio.NewReader(strings.NewReader(longLine)), limits)
	if !errors.Is(err, ErrRequestLineTooLarge) {
		t.Fatalf("request line error %v", err)
	}

	wide := "GET / HTTP/1.1\r\nHost: " + strings.Repeat("h", 40) + "\r\n\r\n"
	_, err = ReadRequest(bufio.NewReader(strings.NewReader(wide)), limits)
	if !errors.Is(err, ErrHeadersTooLarge) {
		t.Fatalf("header error %v", err)
	}

	big := "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 5\r\n\r\nhello"
	_, err = ReadRequest(bufio.NewReader(strings.NewReader(big)), Limits{MaxRequestLine: 64, MaxHeaders: 128, MaxBody: 4})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("body error %v", err)
	}
}

func TestReadRequestRepeatedHeaders(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: localhost\r\nAccept: text/plain\r\nAccept: application/json\r\n\r\n"
	req := readRaw(t, raw, Limits{})
	got := req.Header.Values("Accept")
	if len(got) != 2 || got[0] != "text/plain" || got[1] != "application/json" {
		t.Fatalf("Accept %#v", got)
	}
}

func TestRequestContext(t *testing.T) {
	req := &Request{}
	if req.Context().Err() != nil {
		t.Fatal("default context is cancelled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	req.SetContext(ctx)
	cancel()
	if req.Context().Err() == nil {
		t.Fatal("cancelled context was not visible")
	}
}

func TestCanonicalHeader(t *testing.T) {
	if CanonicalHeader("content-length") != "Content-Length" {
		t.Fatalf("canonical %q", CanonicalHeader("content-length"))
	}
	if CanonicalHeader("HOST") != "Host" {
		t.Fatalf("canonical %q", CanonicalHeader("HOST"))
	}
}

func readRaw(t *testing.T, raw string, limits Limits) *Request {
	t.Helper()
	return readFrom(t, strings.NewReader(raw), limits)
}

func readFrom(t *testing.T, r io.Reader, limits Limits) *Request {
	t.Helper()
	req, err := ReadRequest(bufio.NewReader(r), limits)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

type oneByteReader struct {
	r io.Reader
}

func (b oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return b.r.Read(p[:1])
}

func TestReadRequestLineAtLimit(t *testing.T) {
	// "GET / HTTP/1.1\r\n" is 16 bytes and fits a 16 byte request line.
	const line = "GET / HTTP/1.1\r\n"
	if len(line) != 16 {
		t.Fatalf("line length %d", len(line))
	}
	raw := line + "Host: a\r\n\r\n"
	req := readRaw(t, raw, Limits{MaxRequestLine: 16, MaxHeaders: 64, MaxBody: 8})
	if req.Path != "/" {
		t.Fatalf("path %q", req.Path)
	}
}
