package transport

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExchangeGreetingAndEcho(t *testing.T) {
	cases := []struct {
		name string
		send string
		want string
	}{
		{name: "greeting", send: "Hello Server\n", want: "Hello Client\n"},
		{name: "greeting crlf", send: "Hello Server\r\n", want: "Hello Client\n"},
		{name: "echo", send: "ping\n", want: "Echo: ping\n"},
		{name: "echo crlf", send: "ping\r\n", want: "Echo: ping\n"},
		{name: "empty line", send: "\n", want: "Echo: \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exchangeLine(t, tc.send, DefaultLimits())
			if got != tc.want {
				t.Fatalf("reply %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExchangeSplitWrites(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	errc := startExchange(server, DefaultLimits())
	if _, err := client.Write([]byte("Hello ")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("Server\n")); err != nil {
		t.Fatal(err)
	}

	got := readReply(t, client)
	if got != "Hello Client\n" {
		t.Fatalf("reply %q, want %q", got, "Hello Client\n")
	}
	if err := waitExchange(t, errc); err != nil {
		t.Fatal(err)
	}
}

func TestExchangeMessageTooLarge(t *testing.T) {
	const max = 32
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	limits := Limits{MaxMessage: max, ReadTimeout: time.Second, WriteTimeout: time.Second}
	errc := startExchange(server, limits)

	payload := bytes.Repeat([]byte("A"), max)
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write(payload)
		writeDone <- err
	}()

	err := waitExchange(t, errc)
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("error %v, want %v", err, ErrMessageTooLarge)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}

	// The oversized line is not answered, and the connection stays open
	// until the caller closes it. Exchange itself does not write a reply.
	_ = client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 8)
	n, readErr := client.Read(buf)
	if n != 0 || !isTimeout(readErr) {
		t.Fatalf("read %d bytes, err %v; want a timeout and no reply", n, readErr)
	}
}

func TestExchangeSmallLimit(t *testing.T) {
	limits := Limits{MaxMessage: 8, ReadTimeout: time.Second, WriteTimeout: time.Second}
	got := exchangeLine(t, "abcdefg\n", limits)
	if got != "Echo: abcdefg\n" {
		t.Fatalf("reply %q", got)
	}

	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	errc := startExchange(server, limits)
	go func() {
		_, _ = client.Write([]byte("abcdefgh"))
	}()
	if err := waitExchange(t, errc); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("error %v, want %v", err, ErrMessageTooLarge)
	}
}

func TestExchangeLineAtLimit(t *testing.T) {
	const max = 16
	body := strings.Repeat("B", max-1) // newline occupies the last allowed byte
	got := exchangeLine(t, body+"\n", Limits{
		MaxMessage:   max,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	want := "Echo: " + body + "\n"
	if got != want {
		t.Fatalf("reply %q, want %q", got, want)
	}
}

func TestExchangeClientDropsPartialLine(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	errc := startExchange(server, DefaultLimits())
	if _, err := client.Write([]byte("Hi")); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	err := waitExchange(t, errc)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestExchangeReadTimeout(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	errc := startExchange(server, Limits{
		MaxMessage:   64,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: time.Second,
	})
	err := waitExchange(t, errc)
	if !isTimeout(err) && !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error %v, want a read deadline", err)
	}
}

func TestWriteAllShortWrites(t *testing.T) {
	var dst bytes.Buffer
	err := writeAll(oneByteWriter{dst: &dst}, []byte("Hello Client\n"))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != "Hello Client\n" {
		t.Fatalf("wrote %q", dst.String())
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

func DefaultLimits() Limits {
	return Limits{
		MaxMessage:   DefaultMaxMessage,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}
}

func exchangeLine(t *testing.T, send string, limits Limits) string {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	errc := startExchange(server, limits)
	if _, err := client.Write([]byte(send)); err != nil {
		t.Fatal(err)
	}
	got := readReply(t, client)
	if err := waitExchange(t, errc); err != nil {
		t.Fatal(err)
	}
	return got
}

func startExchange(server net.Conn, limits Limits) <-chan error {
	errc := make(chan error, 1)
	go func() {
		errc <- Exchange(server, limits)
	}()
	return errc
}

func waitExchange(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("exchange did not return")
	}
	return nil
}

func readReply(t *testing.T, r io.Reader) string {
	t.Helper()
	br := bufio.NewReader(r)
	done := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, err := br.ReadString('\n')
		done <- struct {
			line string
			err  error
		}{line, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatal(res.err)
		}
		return res.line
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for reply")
	}
	return ""
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
