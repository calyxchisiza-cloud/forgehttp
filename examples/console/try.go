package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"forgehttp/internal/protocol/http1"
)

type headerField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type tryRequest struct {
	Mode    string        `json:"mode"`
	Method  string        `json:"method"`
	Target  string        `json:"target"`
	Proto   string        `json:"proto"`
	Headers []headerField `json:"headers"`
	Body    string        `json:"body"`
}

type exchange struct {
	RawRequest  string `json:"rawRequest"`
	RawResponse string `json:"rawResponse"`
	Status      string `json:"status"`
	Body        string `json:"body"`
}

type tryResult struct {
	Exchanges []exchange `json:"exchanges"`
	Error     string     `json:"error,omitempty"`
}

func tryHandler(addr string) func(*http1.Request) *http1.Response {
	return func(req *http1.Request) *http1.Response {
		var in tryRequest
		if err := json.Unmarshal(req.Body, &in); err != nil {
			return http1.JSONData(400, tryResult{Error: "request JSON is invalid"})
		}
		result, err := runTry(addr, in)
		if err != nil && len(result.Exchanges) == 0 {
			result.Error = err.Error()
			return http1.JSONData(400, result)
		}
		if err != nil {
			result.Error = err.Error()
		}
		return http1.JSONData(200, result)
	}
}

func runTry(addr string, in tryRequest) (tryResult, error) {
	mode := in.Mode
	if mode == "" {
		mode = "once"
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = "GET"
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		target = "/"
	}
	proto := strings.TrimSpace(in.Proto)
	if proto == "" {
		proto = "HTTP/1.1"
	}
	if mode == "http10" {
		proto = "HTTP/1.0"
	}
	if !tokenOK(method) || !targetOK(target) || (proto != "HTTP/1.0" && proto != "HTTP/1.1") {
		return tryResult{}, fmt.Errorf("method, target, or version is not allowed")
	}
	headers := append([]headerField(nil), in.Headers...)
	if mode == "close" {
		headers = append(headers, headerField{Name: "Connection", Value: "close"})
	}
	for _, h := range headers {
		if !tokenOK(h.Name) || !valueOK(h.Value) {
			return tryResult{}, fmt.Errorf("a header is not allowed")
		}
	}
	if !valueOK(in.Body) {
		return tryResult{}, fmt.Errorf("the body contains a line break that would change the framing")
	}

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return tryResult{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if mode == "stall" {
		raw := method + " " + target + " " + proto + "\r\n"
		if _, err := conn.Write([]byte(raw)); err != nil {
			return tryResult{}, err
		}
		ex, err := readExchange(conn, raw)
		if err != nil {
			return tryResult{Exchanges: []exchange{ex}}, err
		}
		return tryResult{Exchanges: []exchange{ex}}, nil
	}

	raw := buildRaw(method, target, proto, headers, in.Body)
	times := 1
	if mode == "twice" {
		times = 2
	}
	var result tryResult
	for i := 0; i < times; i++ {
		if _, err := conn.Write([]byte(raw)); err != nil {
			return result, err
		}
		ex, err := readExchange(conn, raw)
		if ex.RawResponse != "" || ex.Status != "" {
			result.Exchanges = append(result.Exchanges, ex)
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func buildRaw(method, target, proto string, headers []headerField, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\r\n", method, target, proto)
	wroteLength := false
	for _, h := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", h.Name, h.Value)
		if strings.EqualFold(h.Name, "Content-Length") {
			wroteLength = true
		}
	}
	if body != "" && !wroteLength {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

func readExchange(conn net.Conn, rawRequest string) (exchange, error) {
	br := bufio.NewReader(conn)
	var raw bytes.Buffer
	status, err := br.ReadString('\n')
	if err != nil {
		return exchange{RawRequest: rawRequest}, err
	}
	raw.WriteString(status)
	length := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return exchange{RawRequest: rawRequest, RawResponse: raw.String(), Status: status}, err
		}
		raw.WriteString(line)
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			length, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length: ")))
			if err != nil {
				return exchange{RawRequest: rawRequest, RawResponse: raw.String(), Status: status}, err
			}
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(br, buf); err != nil {
		return exchange{RawRequest: rawRequest, RawResponse: raw.String(), Status: status}, err
	}
	raw.Write(buf)
	return exchange{RawRequest: rawRequest, RawResponse: raw.String(), Status: status, Body: string(buf)}, nil
}

func tokenOK(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7F || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(c)) {
			return false
		}
	}
	return true
}

func targetOK(s string) bool {
	if s == "" || strings.ContainsAny(s, "\r\n ") {
		return false
	}
	return true
}

func valueOK(s string) bool {
	return !strings.ContainsAny(s, "\r\n\x00")
}
