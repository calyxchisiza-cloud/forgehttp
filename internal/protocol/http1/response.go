package http1

import "errors"

// HelloMessage is the sample JSON body. It is 34 bytes.
const HelloMessage = `{"message":"Hello from ForgeHTTP"}`

// Response is one HTTP/1.1 response. Content-Length is computed from Body when the response is written.
type Response struct {
	Status int
	Header Header
	Body   []byte
}

var (
	// ErrBadStatus means the status code is outside 100-599.
	ErrBadStatus = errors.New("invalid status code")

	// ErrBadResponseHeader means a response header name or value is not safe to send.
	ErrBadResponseHeader = errors.New("invalid response header")
)

// Text builds a plain-text response.
func Text(status int, body string) *Response {
	return &Response{
		Status: status,
		Header: Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:   []byte(body),
	}
}

// JSON builds a response whose body is already JSON bytes.
func JSON(status int, body []byte) *Response {
	return &Response{
		Status: status,
		Header: Header{"Content-Type": {"application/json"}},
		Body:   body,
	}
}

var statusText = map[int]string{
	200: "OK",
	201: "Created",
	204: "No Content",
	400: "Bad Request",
	401: "Unauthorized",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	408: "Request Timeout",
	411: "Length Required",
	413: "Content Too Large",
	414: "URI Too Long",
	431: "Request Header Fields Too Large",
	500: "Internal Server Error",
	501: "Not Implemented",
	505: "HTTP Version Not Supported",
}

func reasonPhrase(status int) string {
	if text, ok := statusText[status]; ok {
		return text
	}
	return "Status"
}
