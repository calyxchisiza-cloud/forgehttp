package http1

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestHelloMessageLength(t *testing.T) {
	if len(HelloMessage) != 34 {
		t.Fatalf("HelloMessage is %d bytes", len(HelloMessage))
	}
}

func TestWriteHelloJSON(t *testing.T) {
	var buf bytes.Buffer
	resp := JSON(200, []byte(HelloMessage))
	resp.Header["Content-Length"] = []string{"36"}
	if err := resp.Write(&buf); err != nil {
		t.Fatal(err)
	}
	const want = "HTTP/1.1 200 OK\r\n" +
		"Content-Type: application/json\r\n" +
		"Connection: close\r\n" +
		"Content-Length: 34\r\n" +
		"\r\n" +
		`{"message":"Hello from ForgeHTTP"}`
	if buf.String() != want {
		t.Fatalf("serialized\n%q\nwant\n%q", buf.String(), want)
	}
	if strings.Contains(buf.String(), "Content-Length: 36") {
		t.Fatal("caller Content-Length was sent")
	}
}

func TestWriteErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(400, "bad request").Write(&buf); err != nil {
		t.Fatal(err)
	}
	const want = "HTTP/1.1 400 Bad Request\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Connection: close\r\n" +
		"Content-Length: 11\r\n" +
		"\r\n" +
		"bad request"
	if buf.String() != want {
		t.Fatalf("serialized\n%q", buf.String())
	}
}

func TestWriteKeepAliveIgnoresCallerConnection(t *testing.T) {
	var buf bytes.Buffer
	resp := Text(200, "hi")
	resp.Persist = true
	resp.Header["Connection"] = []string{"close"}
	if err := resp.Write(&buf); err != nil {
		t.Fatal(err)
	}
	const want = "HTTP/1.1 200 OK\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Connection: keep-alive\r\n" +
		"Content-Length: 2\r\n" +
		"\r\n" +
		"hi"
	if buf.String() != want {
		t.Fatalf("serialized\n%q", buf.String())
	}
}

func TestWriteRejectsHeaderInjection(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "crlf in value", key: "X-Test", value: "ok\r\nInjected: yes"},
		{name: "nul in value", key: "X-Test", value: "ok\x00"},
		{name: "space in name", key: "X Test", value: "ok"},
		{name: "transfer encoding", key: "Transfer-Encoding", value: "chunked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := Text(200, "hi")
			resp.Header[tc.key] = []string{tc.value}
			var buf bytes.Buffer
			err := resp.Write(&buf)
			if !errors.Is(err, ErrBadResponseHeader) {
				t.Fatalf("error %v", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("wrote %q after a rejected header", buf.String())
			}
		})
	}
}

func TestWriteRejectsBadStatus(t *testing.T) {
	var buf bytes.Buffer
	err := (&Response{Status: 99, Body: []byte("x")}).Write(&buf)
	if !errors.Is(err, ErrBadStatus) || buf.Len() != 0 {
		t.Fatalf("error %v, wrote %d", err, buf.Len())
	}
}

func TestWriteShortWrites(t *testing.T) {
	var dst bytes.Buffer
	resp := JSON(200, []byte(HelloMessage))
	if err := resp.Write(oneByteWriter{dst: &dst}); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := resp.Write(&want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst.Bytes(), want.Bytes()) {
		t.Fatalf("short write\n%q\nwant\n%q", dst.Bytes(), want.Bytes())
	}
}

type oneByteWriter struct {
	dst *bytes.Buffer
}

func (w oneByteWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.dst.WriteByte(p[0])
	return 1, nil
}
