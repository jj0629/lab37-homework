// Package mockapi simulates the upstream polling API and the robot.
//
// Polling API: each line of api_responses.jsonl becomes available at
// start + i*step. GET /orders?time_since=T returns the first available line
// newer than T, with as_of (the cursor to send next) and has_more. Lines
// recorded as 500s fail the first time they are served and succeed on retry
// (with the same data), which models a transient upstream error.
//
// Robot: POST /robot/dispatch and /robot/dispatch/cancel accept payloads,
// dedupe by Idempotency-Key, and fail a configurable fraction of requests.
package mockapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"time"
)

type line struct {
	Response int             `json:"response"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// Server is the mock upstream + robot.
type Server struct {
	mu       sync.Mutex
	lines    []line
	step     time.Duration
	start    time.Time
	failed   map[int]bool
	served   int
	errors   int
	failRate float64
	robot    map[string]int // idempotency key -> times received
	robotErr int
}

// New loads the sample responses.
func New(path string, step time.Duration, robotFailRate float64) (*Server, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := &Server{step: step, start: time.Now(), failed: map[int]bool{}, failRate: robotFailRate, robot: map[string]int{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("line %d: %w", len(s.lines)+1, err)
		}
		s.lines = append(s.lines, l)
	}
	return s, sc.Err()
}

func (s *Server) availableAt(i int) time.Time { return s.start.Add(time.Duration(i) * s.step) }

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", s.orders)
	mux.HandleFunc("GET /admin/status", s.status)
	mux.HandleFunc("POST /admin/reset", s.reset)
	mux.HandleFunc("POST /admin/release-all", s.releaseAll)
	mux.HandleFunc("POST /robot/dispatch", s.robotHandler("dispatch"))
	mux.HandleFunc("POST /robot/dispatch/cancel", s.robotHandler("cancel"))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }) //nolint:errcheck
	return mux
}

func (s *Server) orders(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if v := r.URL.Query().Get("time_since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeJSON(w, 400, map[string]any{"response": 400, "error": "time_since must be RFC3339"})
			return
		}
		since = t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	idx := -1
	for i := range s.lines {
		at := s.availableAt(i)
		if at.After(since) && !at.After(now) {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Nothing new: an empty delta, cursor moves to now (or stays if ahead).
		asOf := now
		if since.After(now) {
			asOf = since
		}
		writeJSON(w, 200, map[string]any{"response": 200, "data": map[string]any{}, "as_of": asOf.UTC().Format(time.RFC3339Nano), "has_more": false})
		return
	}
	l := s.lines[idx]
	s.served++
	hasMore := idx+1 < len(s.lines) && !s.availableAt(idx+1).After(now)
	body := map[string]any{"response": l.Response, "as_of": s.availableAt(idx).UTC().Format(time.RFC3339Nano), "has_more": hasMore, "line": idx + 1}
	if len(l.Data) > 0 {
		body["data"] = l.Data
	} else {
		body["data"] = map[string]any{}
	}
	if l.Response >= 500 && !s.failed[idx] {
		s.failed[idx] = true
		s.errors++
		body["error"] = l.Error
		if body["error"] == "" {
			body["error"] = "Internal Server Error"
		}
		delete(body, "as_of")
		delete(body, "has_more")
		writeJSON(w, l.Response, body)
		return
	}
	body["response"] = 200
	writeJSON(w, 200, body)
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	released := 0
	now := time.Now()
	for i := range s.lines {
		if !s.availableAt(i).After(now) {
			released++
		}
	}
	dispatches := 0
	for _, n := range s.robot {
		dispatches += n
	}
	writeJSON(w, 200, map[string]any{
		"lines": len(s.lines), "released": released, "step_seconds": s.step.Seconds(),
		"started_at": s.start, "served": s.served, "errors_served": s.errors,
		"robot": map[string]any{"unique_messages": len(s.robot), "requests": dispatches, "injected_failures": s.robotErr},
	})
}

func (s *Server) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.start, s.failed, s.served, s.errors = time.Now(), map[int]bool{}, 0, 0
	s.robot, s.robotErr = map[string]int{}, 0
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"reset": true})
}

func (s *Server) releaseAll(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.start = time.Now().Add(-time.Duration(len(s.lines)) * s.step)
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"released": true})
}

func (s *Server) robotHandler(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			writeJSON(w, 400, map[string]any{"error": "Idempotency-Key header required"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if rand.Float64() < s.failRate {
			s.robotErr++
			writeJSON(w, 503, map[string]any{"error": "robot busy, retry"})
			return
		}
		s.robot[key]++
		writeJSON(w, 200, map[string]any{
			"accepted": true, "kind": kind, "robot_job_id": "job-" + key,
			"duplicate": s.robot[key] > 1, "station": fmt.Sprintf("assembly-%d", 1+len(s.robot)%4),
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
