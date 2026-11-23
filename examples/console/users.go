package main

import (
	"encoding/json"
	"strings"
	"sync"

	"forgehttp/internal/protocol/http1"
	"forgehttp/internal/server"
)

type user struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type userStore struct {
	mu    sync.Mutex
	next  int
	users []user
}

func newUserStore() *userStore {
	return &userStore{next: 1, users: []user{}}
}

func registerUsers(srv *server.Server, store *userStore) error {
	if err := srv.Handle("GET", "/api/users", store.list); err != nil {
		return err
	}
	return srv.Handle("POST", "/api/users", store.create)
}

func (s *userStore) list(req *http1.Request) *http1.Response {
	if err := req.Context().Err(); err != nil {
		return http1.Text(503, "shutting down")
	}
	s.mu.Lock()
	out := append([]user{}, s.users...)
	s.mu.Unlock()
	return http1.JSONData(200, out)
}

func (s *userStore) create(req *http1.Request) *http1.Response {
	if err := req.Context().Err(); err != nil {
		return http1.Text(503, "shutting down")
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		return http1.Text(400, "name is required")
	}
	s.mu.Lock()
	created := user{ID: s.next, Name: strings.TrimSpace(body.Name)}
	s.next++
	s.users = append(s.users, created)
	s.mu.Unlock()
	return http1.JSONData(201, created)
}
