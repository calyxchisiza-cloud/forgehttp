// Package router matches a request method and exact path to a handler.
package router

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"forgehttp/internal/protocol/http1"
)

// Handler handles one request that already matched a route.
type Handler func(*http1.Request) *http1.Response

// ErrDuplicateRoute means the same method and path were registered twice.
var ErrDuplicateRoute = errors.New("route already registered")

// ErrBadRoute means the method or path cannot be registered.
var ErrBadRoute = errors.New("invalid route")

// Router stores exact routes. Paths are matched without the query string.
type Router struct {
	mu     sync.RWMutex
	routes map[string]map[string]Handler
}

// New returns an empty router.
func New() *Router {
	return &Router{routes: make(map[string]map[string]Handler)}
}

// Handle registers h for an uppercase method and an exact origin path.
func (r *Router) Handle(method, path string, h Handler) error {
	if !validMethod(method) || !validPath(path) || h == nil {
		return ErrBadRoute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byMethod := r.routes[path]
	if byMethod == nil {
		byMethod = make(map[string]Handler)
		r.routes[path] = byMethod
	}
	if _, exists := byMethod[method]; exists {
		return ErrDuplicateRoute
	}
	byMethod[method] = h
	return nil
}

// Serve runs the handler for req. An unknown path is 404. A known path with
// a different method is 405 and includes an Allow header.
func (r *Router) Serve(req *http1.Request) *http1.Response {
	r.mu.RLock()
	byMethod := r.routes[req.Path]
	handler := byMethod[req.Method]
	methods := make([]string, 0, len(byMethod))
	for method := range byMethod {
		methods = append(methods, method)
	}
	r.mu.RUnlock()

	if handler != nil {
		resp := handler(req)
		if resp == nil {
			return http1.Text(500, "nil response")
		}
		return resp
	}
	if len(methods) == 0 {
		return http1.Text(404, "not found")
	}
	sort.Strings(methods)
	resp := http1.Text(405, "method not allowed")
	resp.Header["Allow"] = []string{strings.Join(methods, ", ")}
	return resp
}

func validMethod(method string) bool {
	if method == "" {
		return false
	}
	for i := 0; i < len(method); i++ {
		if method[i] < 'A' || method[i] > 'Z' {
			return false
		}
	}
	return true
}

func validPath(path string) bool {
	if path == "" || path[0] != '/' {
		return false
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c <= ' ' || c == 0x7F || c == '?' || c == '#' {
			return false
		}
	}
	return true
}
