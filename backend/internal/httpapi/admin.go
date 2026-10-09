package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jj0629/lab37-homework/backend/internal/ingest"
	"github.com/jj0629/lab37-homework/backend/internal/normalize"
	"github.com/jj0629/lab37-homework/backend/internal/poller"
)

// ingestWebhook is the webhook edge. It accepts a single JSON object, a JSON
// array, or newline-delimited JSON, does minimal validation, writes each
// payload to the inbox and returns 202. All real work happens in workers.
func (s *Server) ingestWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		writeErr(w, 413, "body too large (max 5 MiB)")
		return
	}
	var payloads []json.RawMessage
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &payloads); err != nil {
			writeErr(w, 400, "invalid JSON array: "+err.Error())
			return
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		for dec.More() {
			var m json.RawMessage
			if err := dec.Decode(&m); err != nil {
				writeErr(w, 400, "invalid JSON: "+err.Error())
				return
			}
			payloads = append(payloads, m)
		}
	}
	if len(payloads) == 0 {
		writeErr(w, 400, "empty body")
		return
	}
	type rejected struct {
		Index int    `json:"index"`
		Error string `json:"error"`
	}
	res := struct {
		Accepted   int        `json:"accepted"`
		Duplicates int        `json:"duplicates"`
		Rejected   []rejected `json:"rejected"`
	}{Rejected: []rejected{}}
	for i, p := range payloads {
		e, err := ingest.EnqueueWebhook(r.Context(), s.DB, p)
		if err != nil {
			if errors.Is(err, normalize.ErrMissingID) || strings.HasPrefix(err.Error(), "invalid json") {
				res.Rejected = append(res.Rejected, rejected{i, err.Error()})
				continue
			}
			internal(w, r, err)
			return
		}
		res.Accepted++
		if e.Duplicate {
			res.Duplicates++
		}
	}
	code := 202
	if res.Accepted == 0 {
		code = 400
	}
	writeJSON(w, code, res)
}

// importCSV accepts a multipart upload (field "file") of a survey export.
func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxCSVBytes+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, `expected multipart form with a "file" field (max `+fmt.Sprint(s.Cfg.MaxCSVBytes>>20)+` MiB)`)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, s.Cfg.MaxCSVBytes+1))
	if err != nil {
		writeErr(w, 400, "could not read file")
		return
	}
	if int64(len(data)) > s.Cfg.MaxCSVBytes {
		writeErr(w, 413, "file too large")
		return
	}
	s.runImport(w, r, hdr.Filename, data)
}

func (s *Server) runImport(w http.ResponseWriter, r *http.Request, name string, data []byte) {
	sum := sha256.Sum256(data)
	res, err := ingest.ImportCSV(r.Context(), s.DB, name, bytes.NewReader(data), hex.EncodeToString(sum[:]), s.Cfg.MealTimes)
	if errors.Is(err, normalize.ErrBadHeader) {
		writeErr(w, 400, err.Error())
		return
	}
	if err != nil {
		internal(w, r, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	byStatus := map[string]int{}
	bySource := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT 's', status, count(*) FROM orders GROUP BY status
		UNION ALL SELECT 'o', source, count(*) FROM orders GROUP BY source`)
	if err != nil {
		internal(w, r, err)
		return
	}
	for rows.Next() {
		var kind, k string
		var n int
		if err := rows.Scan(&kind, &k, &n); err != nil {
			rows.Close()
			internal(w, r, err)
			return
		}
		if kind == "s" {
			byStatus[k] = n
		} else {
			bySource[k] = n
		}
	}
	rows.Close()
	var st struct {
		Total, NeedsReview, InboxPending, InboxRetrying, InboxDead, DeadLetterOpen int
		Processed1m, Received1m, OutboxPending, OutboxFailed, Scheduled            int
		Now                                                                        time.Time
		Offset                                                                     string
	}
	err = s.DB.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM orders WHERE needs_review),
		(SELECT count(*) FROM inbox WHERE status='pending'),
		(SELECT count(*) FROM inbox WHERE status='pending' AND attempts > 0),
		(SELECT count(*) FROM inbox WHERE status='dead'),
		(SELECT count(*) FROM dead_letter WHERE resolved_at IS NULL),
		(SELECT count(*) FROM inbox WHERE processed_at > now() - interval '1 minute'),
		(SELECT count(*) FROM inbox WHERE received_at > now() - interval '1 minute'),
		(SELECT count(*) FROM dispatch_outbox WHERE status='pending'),
		(SELECT count(*) FROM dispatch_outbox WHERE status='failed'),
		(SELECT count(*) FROM orders WHERE status='scheduled'),
		app_now(),
		(SELECT value FROM app_settings WHERE key='clock_offset')`).
		Scan(&st.Total, &st.NeedsReview, &st.InboxPending, &st.InboxRetrying, &st.InboxDead, &st.DeadLetterOpen,
			&st.Processed1m, &st.Received1m, &st.OutboxPending, &st.OutboxFailed, &st.Scheduled, &st.Now, &st.Offset)
	if err != nil {
		internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"total": st.Total, "by_status": byStatus, "by_source": bySource, "needs_review": st.NeedsReview,
		"inbox":            map[string]int{"pending": st.InboxPending, "retrying": st.InboxRetrying, "dead": st.InboxDead},
		"dead_letter_open": st.DeadLetterOpen,
		"throughput":       map[string]int{"processed_last_min": st.Processed1m, "received_last_min": st.Received1m},
		"outbox":           map[string]int{"pending": st.OutboxPending, "failed": st.OutboxFailed},
		"clock":            map[string]any{"now": st.Now, "offset": st.Offset},
	})
}

// DeadLetter is a quarantined or failed payload.
type DeadLetter struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	ExternalID string          `json:"external_id"`
	Reason     string          `json:"reason"`
	Detail     string          `json:"detail"`
	Payload    json.RawMessage `json:"payload"`
	OrderID    *int64          `json:"order_id"`
	CreatedAt  time.Time       `json:"created_at"`
	ResolvedAt *time.Time      `json:"resolved_at"`
}

func (s *Server) queryDeadLetter(r *http.Request, where string, args ...any) ([]DeadLetter, error) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, source, external_id, reason, detail, payload, order_id, created_at, resolved_at
		FROM dead_letter`+where+` ORDER BY id DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeadLetter{}
	for rows.Next() {
		var d DeadLetter
		var p []byte
		var oid sql.NullInt64
		var res sql.NullTime
		if err := rows.Scan(&d.ID, &d.Source, &d.ExternalID, &d.Reason, &d.Detail, &p, &oid, &d.CreatedAt, &res); err != nil {
			return nil, err
		}
		if len(p) > 0 {
			d.Payload = p
		}
		if oid.Valid {
			d.OrderID = &oid.Int64
		}
		if res.Valid {
			d.ResolvedAt = &res.Time
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Server) listDeadLetter(w http.ResponseWriter, r *http.Request) {
	where := " WHERE resolved_at IS NULL"
	if r.URL.Query().Get("include_resolved") == "true" {
		where = ""
	}
	items, err := s.queryDeadLetter(r, where)
	if err != nil {
		internal(w, r, err)
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) resolveDeadLetter(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE dead_letter SET resolved_at=now() WHERE id=$1 AND resolved_at IS NULL`, id)
	if err != nil {
		internal(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 404, "not found or already resolved")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "resolved": true})
}

func (s *Server) ingestionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var cur struct {
		Cursor, LastStatus, LastError, Leader string
		LastPolled                            sql.NullTime
		Errors                                int
	}
	_ = s.DB.QueryRowContext(ctx, `SELECT cursor, last_status, last_error, leader, last_polled_at, consecutive_errors
		FROM ingest_cursors WHERE name=$1`, poller.CursorName).
		Scan(&cur.Cursor, &cur.LastStatus, &cur.LastError, &cur.Leader, &cur.LastPolled, &cur.Errors)
	var polled *time.Time
	if cur.LastPolled.Valid {
		polled = &cur.LastPolled.Time
	}

	leaders := []map[string]any{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT role, holder, since, heartbeat, heartbeat > now() - interval '10 seconds' FROM leaders ORDER BY role`); err == nil {
		for rows.Next() {
			var role, holder string
			var since, hb time.Time
			var alive bool
			if rows.Scan(&role, &holder, &since, &hb, &alive) == nil {
				leaders = append(leaders, map[string]any{"role": role, "holder": holder, "since": since, "heartbeat": hb, "alive": alive})
			}
		}
		rows.Close()
	}

	uploads := []map[string]any{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT id, filename, row_count, new_count, dup_count, jsonb_array_length(rejected), created_at
		FROM csv_uploads ORDER BY id DESC LIMIT 10`); err == nil {
		for rows.Next() {
			var id int64
			var name string
			var n, nw, dup, rej int
			var at time.Time
			if rows.Scan(&id, &name, &n, &nw, &dup, &rej, &at) == nil {
				uploads = append(uploads, map[string]any{"id": id, "filename": name, "rows": n, "new": nw, "duplicate": dup, "rejected": rej, "created_at": at})
			}
		}
		rows.Close()
	}

	inbox := map[string]map[string]int{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT source, CASE WHEN is_duplicate THEN 'duplicate' ELSE status END, count(*) FROM inbox GROUP BY 1, 2`); err == nil {
		for rows.Next() {
			var src, st string
			var n int
			if rows.Scan(&src, &st, &n) == nil {
				if inbox[src] == nil {
					inbox[src] = map[string]int{}
				}
				inbox[src][st] = n
			}
		}
		rows.Close()
	}

	var mock any
	mctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if req, err := http.NewRequestWithContext(mctx, http.MethodGet, s.Cfg.MockAPIURL+"/admin/status", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&mock)
			resp.Body.Close()
		} else {
			mock = map[string]string{"error": "mock API unreachable"}
		}
	}

	writeJSON(w, 200, map[string]any{
		"poller": map[string]any{"cursor": cur.Cursor, "last_status": cur.LastStatus, "last_error": cur.LastError,
			"leader": cur.Leader, "last_polled_at": polled, "consecutive_errors": cur.Errors},
		"leaders": leaders, "uploads": uploads, "inbox": inbox, "mock_api": mock,
	})
}

// timeWarp shifts app_now() for demos: {"minutes": N}, {"to_next_release": true} or {"reset": true}.
func (s *Server) timeWarp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Minutes       int  `json:"minutes"`
		ToNextRelease bool `json:"to_next_release"`
		Reset         bool `json:"reset"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	ctx := r.Context()
	var err error
	switch {
	case body.Reset:
		_, err = s.DB.ExecContext(ctx, `UPDATE app_settings SET value='0 seconds' WHERE key='clock_offset'`)
	case body.ToNextRelease:
		var res sql.Result
		res, err = s.DB.ExecContext(ctx, `UPDATE app_settings SET value = (
			(SELECT min(scheduled_for) FROM orders WHERE status='scheduled') - make_interval(mins => $1::int) - now() + interval '1 second'
			)::text WHERE key='clock_offset' AND EXISTS (SELECT 1 FROM orders WHERE status='scheduled')
			AND (SELECT min(scheduled_for) FROM orders WHERE status='scheduled') - make_interval(mins => $1::int) > app_now()`, s.Cfg.LeadMinutes)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				writeErr(w, 409, "no future scheduled orders to jump to")
				return
			}
		}
	case body.Minutes != 0:
		_, err = s.DB.ExecContext(ctx, `UPDATE app_settings SET value = (value::interval + make_interval(mins => $1::int))::text
			WHERE key='clock_offset'`, body.Minutes)
	default:
		writeErr(w, 400, `send {"minutes": N}, {"to_next_release": true} or {"reset": true}`)
		return
	}
	if err != nil {
		internal(w, r, err)
		return
	}
	var now time.Time
	var offset string
	_ = s.DB.QueryRowContext(ctx, `SELECT app_now(), value FROM app_settings WHERE key='clock_offset'`).Scan(&now, &offset)
	writeJSON(w, 200, map[string]any{"now": now, "offset": offset})
}

func (s *Server) pollNow(w http.ResponseWriter, r *http.Request) {
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE ingest_cursors SET poll_requested=true WHERE name=$1`, poller.CursorName); err != nil {
		internal(w, r, err)
		return
	}
	writeJSON(w, 202, map[string]string{"status": "poll requested"})
}

// injectWebhook enqueues webhook payloads from the bundled sample file.
// {"count": N, "fresh": true} picks random sample orders and gives them new
// UUIDs (new orders); {"fresh": false, "offset": K} replays lines K..K+N
// verbatim (duplicates, cancels and conflicts included).
func (s *Server) injectWebhook(w http.ResponseWriter, r *http.Request) {
	body := struct {
		Count  int  `json:"count"`
		Fresh  bool `json:"fresh"`
		Offset int  `json:"offset"`
	}{Count: 10, Fresh: true}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if body.Count < 1 || body.Count > 5000 {
		writeErr(w, 400, "count must be 1..5000")
		return
	}
	lines, err := readLines(filepath.Join(s.Cfg.SampleDir, "webhook_orders.jsonl"))
	if err != nil {
		sampleErr(w, r, err)
		return
	}
	accepted, dups := 0, 0
	for i := 0; i < body.Count; i++ {
		var raw []byte
		if body.Fresh {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(lines))))
			var m map[string]any
			if json.Unmarshal(lines[n.Int64()], &m) != nil {
				continue
			}
			delete(m, "update")
			m["order_id"] = newUUID()
			raw, _ = json.Marshal(m)
		} else {
			idx := body.Offset + i
			if idx >= len(lines) {
				break
			}
			raw = lines[idx]
		}
		e, err := ingest.EnqueueWebhook(r.Context(), s.DB, raw)
		if err != nil {
			continue
		}
		accepted++
		if e.Duplicate {
			dups++
		}
	}
	writeJSON(w, 202, map[string]any{"accepted": accepted, "duplicates": dups, "sample_lines": len(lines)})
}

func (s *Server) listSamples(w http.ResponseWriter, r *http.Request) {
	matches, _ := filepath.Glob(filepath.Join(s.Cfg.SampleDir, "*.csv"))
	out := []map[string]any{}
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"name": filepath.Base(m), "bytes": fi.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	writeJSON(w, 200, out)
}

func (s *Server) importSample(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File string `json:"file"`
	}
	if err := decodeBody(r, &body); err != nil || body.File == "" {
		writeErr(w, 400, `send {"file": "orders_1.csv"}`)
		return
	}
	name := filepath.Base(body.File)
	if !strings.HasSuffix(name, ".csv") {
		writeErr(w, 400, "not a CSV sample")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.Cfg.SampleDir, name))
	if err != nil {
		sampleErr(w, r, err)
		return
	}
	s.runImport(w, r, name, data)
}

// reset wipes all order data and rewinds the mock API (demo convenience).
func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, err := s.DB.ExecContext(ctx, `TRUNCATE order_events, order_items, dispatch_outbox, dead_letter, inbox, csv_uploads, orders RESTART IDENTITY CASCADE;
		UPDATE ingest_cursors SET cursor='', last_status='', last_error='', last_polled_at=NULL, consecutive_errors=0;
		UPDATE app_settings SET value='0 seconds' WHERE key='clock_offset';`)
	if err != nil {
		internal(w, r, err)
		return
	}
	mockReset := "ok"
	if req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Cfg.MockAPIURL+"/admin/reset", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err != nil {
			mockReset = "mock API unreachable"
		} else {
			resp.Body.Close()
		}
	}
	writeJSON(w, 200, map[string]any{"reset": true, "mock_api": mockReset})
}

// sampleErr reports a problem reading the bundled sample data clearly.
func sampleErr(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("sample data", "path", r.URL.Path, "err", err)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeErr(w, 404, "sample file not found in SAMPLE_DIR")
	case errors.Is(err, fs.ErrPermission):
		writeErr(w, 500, "sample file is not readable by the server (permission denied); rebuild the image")
	default:
		writeErr(w, 500, "could not read sample file: "+err.Error())
	}
}

func readLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if b := bytes.TrimSpace(sc.Bytes()); len(b) > 0 {
			out = append(out, append([]byte(nil), b...))
		}
	}
	return out, sc.Err()
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
