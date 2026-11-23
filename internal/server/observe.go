package server

import "sort"

const traceLimit = 40

// ConnView is one open connection, safe to show outside the server lock.
type ConnView struct {
	Remote   string `json:"remote"`
	Stage    string `json:"stage"`
	Requests int    `json:"requests"`
}

// Trace is one completed request the server parsed and answered.
type Trace struct {
	Remote     string `json:"remote"`
	Method     string `json:"method,omitempty"`
	Target     string `json:"target,omitempty"`
	Proto      string `json:"proto,omitempty"`
	Status     int    `json:"status,omitempty"`
	Connection string `json:"connection,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Snapshot is the connection table and the recent traces.
type Snapshot struct {
	MaxConns    int        `json:"maxConns"`
	Active      int        `json:"active"`
	Reading     int        `json:"reading"`
	Serving     int        `json:"serving"`
	Connections []ConnView `json:"connections"`
	Traces      []Trace    `json:"traces"`
}

// Snapshot copies the current connection table and recent traces.
func (s *Server) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Snapshot{
		MaxConns:    s.maxConns(),
		Connections: []ConnView{},
		Traces:      append([]Trace(nil), s.traces...),
	}
	for _, st := range s.conns {
		view := ConnView{Remote: st.remote, Stage: "reading", Requests: st.requests}
		if st.stage == stageServing {
			view.Stage = "serving"
			out.Serving++
		} else {
			out.Reading++
		}
		out.Active++
		out.Connections = append(out.Connections, view)
	}
	sort.Slice(out.Connections, func(i, j int) bool {
		return out.Connections[i].Remote < out.Connections[j].Remote
	})
	return out
}

func (s *Server) remember(tr Trace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, tr)
	if len(s.traces) > traceLimit {
		s.traces = append([]Trace(nil), s.traces[len(s.traces)-traceLimit:]...)
	}
}
