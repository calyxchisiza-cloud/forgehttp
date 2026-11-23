package router

import (
	"errors"
	"testing"

	"forgehttp/internal/protocol/http1"
)

func TestExactMatchAndQueryIgnored(t *testing.T) {
	r := New()
	if err := r.Handle("GET", "/api/users", func(req *http1.Request) *http1.Response {
		return http1.Text(200, req.Path)
	}); err != nil {
		t.Fatal(err)
	}
	resp := r.Serve(&http1.Request{Method: "GET", Path: "/api/users", RawQuery: "x=1"})
	if resp.Status != 200 || string(resp.Body) != "/api/users" {
		t.Fatalf("status %d body %q", resp.Status, resp.Body)
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	r := New()
	if err := r.Handle("GET", "/api/users", func(*http1.Request) *http1.Response {
		return http1.Text(200, "list")
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle("POST", "/api/users", func(*http1.Request) *http1.Response {
		return http1.Text(201, "created")
	}); err != nil {
		t.Fatal(err)
	}

	missing := r.Serve(&http1.Request{Method: "GET", Path: "/missing"})
	if missing.Status != 404 || string(missing.Body) != "not found" {
		t.Fatalf("missing status %d body %q", missing.Status, missing.Body)
	}

	wrong := r.Serve(&http1.Request{Method: "DELETE", Path: "/api/users"})
	if wrong.Status != 405 || string(wrong.Body) != "method not allowed" {
		t.Fatalf("wrong status %d body %q", wrong.Status, wrong.Body)
	}
	if wrong.Header.Get("Allow") != "GET, POST" {
		t.Fatalf("Allow %q", wrong.Header.Get("Allow"))
	}
}

func TestDuplicateAndInvalidRoutes(t *testing.T) {
	r := New()
	h := func(*http1.Request) *http1.Response { return http1.Text(200, "ok") }
	if err := r.Handle("GET", "/ok", h); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle("GET", "/ok", h); !errors.Is(err, ErrDuplicateRoute) {
		t.Fatalf("duplicate error %v", err)
	}
	if err := r.Handle("get", "/ok", h); !errors.Is(err, ErrBadRoute) {
		t.Fatalf("lowercase method error %v", err)
	}
	if err := r.Handle("GET", "users", h); !errors.Is(err, ErrBadRoute) {
		t.Fatalf("path error %v", err)
	}
}

func TestNilHandlerResponse(t *testing.T) {
	r := New()
	if err := r.Handle("GET", "/", func(*http1.Request) *http1.Response { return nil }); err != nil {
		t.Fatal(err)
	}
	resp := r.Serve(&http1.Request{Method: "GET", Path: "/"})
	if resp.Status != 500 {
		t.Fatalf("status %d", resp.Status)
	}
}
