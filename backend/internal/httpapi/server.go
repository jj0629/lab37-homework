// Package httpapi exposes the ingest edge, read APIs and demo/admin actions.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jj0629/lab37-homework/backend/internal/config"
)

// Server holds dependencies for the HTTP handlers.
type Server struct {
	DB  *sql.DB
	Cfg config.Config
}

// Handler builds the router (Go 1.22+ pattern routing, no framework).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", s.ready)

	// Ingest edge.
	mux.HandleFunc("POST /api/ingest/webhook", s.ingestWebhook)
	mux.HandleFunc("POST /api/orders/import", s.importCSV)

	// Read APIs.
	mux.HandleFunc("GET /api/orders", s.listOrders)
	mux.HandleFunc("GET /api/orders/export.csv", s.exportCSV)
	mux.HandleFunc("GET /api/orders/{id}", s.getOrder)
	mux.HandleFunc("GET /api/orders/{id}/events", s.getEvents)
	mux.HandleFunc("GET /api/orders/{id}/related", s.getRelated)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/dead-letter", s.listDeadLetter)
	mux.HandleFunc("GET /api/ingestion", s.ingestionStatus)

	// Actions.
	mux.HandleFunc("POST /api/orders/{id}/dispatch", s.dispatchNow)
	mux.HandleFunc("POST /api/orders/{id}/cancel", s.cancelOrder)
	mux.HandleFunc("POST /api/dead-letter/{id}/resolve", s.resolveDeadLetter)

	// Demo / admin.
	mux.HandleFunc("POST /api/admin/time-warp", s.timeWarp)
	mux.HandleFunc("POST /api/admin/poll-now", s.pollNow)
	mux.HandleFunc("POST /api/admin/inject-webhook", s.injectWebhook)
	mux.HandleFunc("GET /api/admin/samples", s.listSamples)
	mux.HandleFunc("POST /api/admin/import-sample", s.importSample)
	mux.HandleFunc("POST /api/admin/reset", s.reset)

	return logRequests(mux)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.PingContext(ctx); err != nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) { s.code = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "path", r.URL.Path, "err", v)
				writeErr(rec, 500, "internal error")
			}
			// Keep logs quiet for the dashboard's polling.
			if r.Method != http.MethodGet || rec.code >= 400 {
				slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.code, "ms", time.Since(start).Milliseconds())
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func decodeBody(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

// internal logs the real error and returns a generic 500.
func internal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("handler error", "path", r.URL.Path, "err", err)
	writeErr(w, 500, "internal error")
}
