// Package poller pulls deltas from the upstream order API (leader only).
package poller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"time"

	"github.com/jj0629/lab37-homework/backend/internal/config"
	"github.com/jj0629/lab37-homework/backend/internal/ingest"
	"github.com/jj0629/lab37-homework/backend/internal/normalize"
)

// CursorName is the ingest_cursors row this poller owns.
const CursorName = "order-api"

// Poller calls GET {MockAPIURL}/orders?time_since=<cursor>.
//
// Fault tolerance:
//   - 200: enqueue every per-order delta, then advance the cursor.
//   - 500 with partial data: enqueue what we got (idempotent) but keep the
//     cursor, so the next call re-asks for the same window.
//   - 500 without data / network error: keep the cursor and back off
//     exponentially with jitter.
//
// Enqueue-then-advance means a crash between the two steps causes a
// re-fetch, which the inbox idempotency key turns into harmless duplicates.
type Poller struct {
	DB       *sql.DB
	Cfg      config.Config
	Client   *http.Client
	Instance string
}

// Run polls until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 10 * time.Second}
	}
	errs := 0
	next := time.Now()
	for ctx.Err() == nil {
		requested := p.pollRequested(ctx)
		if requested || !time.Now().Before(next) {
			more, err := p.PollOnce(ctx)
			if err != nil {
				errs++
				backoff := min(time.Second*time.Duration(1<<min(errs, 6)), 60*time.Second)
				backoff += time.Duration(rand.Int64N(int64(backoff/2) + 1))
				slog.Warn("poll failed", "err", err, "consecutive", errs, "retry_in", backoff.String())
				next = time.Now().Add(backoff)
			} else {
				errs = 0
				next = time.Now().Add(p.Cfg.PollInterval)
				if more {
					next = time.Now()
					continue // catch up without waiting for the next tick
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (p *Poller) pollRequested(ctx context.Context) bool {
	var req bool
	err := p.DB.QueryRowContext(ctx, `UPDATE ingest_cursors SET poll_requested=false
		WHERE name=$1 AND poll_requested RETURNING true`, CursorName).Scan(&req)
	return err == nil && req
}

// PollOnce performs a single poll cycle and reports whether more data is ready.
func (p *Poller) PollOnce(ctx context.Context) (bool, error) {
	var cursor string
	if err := p.DB.QueryRowContext(ctx, `SELECT cursor FROM ingest_cursors WHERE name=$1`, CursorName).Scan(&cursor); err != nil {
		return false, err
	}
	started := time.Now().UTC()
	u := p.Cfg.MockAPIURL + "/orders?time_since=" + url.QueryEscape(cursor)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := p.Client.Do(req)
	if err != nil {
		p.record(ctx, cursor, "network error", err.Error(), true)
		return false, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	resp.Body.Close()
	if err != nil {
		p.record(ctx, cursor, "read error", err.Error(), true)
		return false, err
	}

	var r normalize.APIResponse
	if err := json.Unmarshal(body, &r); err != nil {
		p.record(ctx, cursor, fmt.Sprintf("HTTP %d (bad body)", resp.StatusCode), err.Error(), true)
		return false, fmt.Errorf("decode: %w", err)
	}
	if r.Response == 0 {
		r.Response = resp.StatusCode
	}

	deltas := normalize.GroupByOrder(r.Data)
	for _, d := range deltas {
		b, _ := json.Marshal(d)
		if _, err := ingest.Enqueue(ctx, p.DB, "api", d.ExternalID(), b, nil); err != nil {
			p.record(ctx, cursor, "enqueue error", err.Error(), true)
			return false, err
		}
	}

	if r.Response >= 200 && r.Response < 300 {
		nextCursor := r.AsOf
		if nextCursor == "" {
			nextCursor = started.Format(time.RFC3339Nano)
		}
		p.record(ctx, nextCursor, fmt.Sprintf("200 OK (%d orders)", len(deltas)), "", false)
		return r.HasMore, nil
	}
	status := fmt.Sprintf("%d, cursor held", r.Response)
	if len(deltas) > 0 {
		status = fmt.Sprintf("%d with partial data (%d orders applied), cursor held", r.Response, len(deltas))
	}
	p.record(ctx, cursor, status, r.Error, true)
	return false, fmt.Errorf("upstream %d: %s", r.Response, r.Error)
}

func (p *Poller) record(ctx context.Context, cursor, status, errMsg string, failed bool) {
	_, err := p.DB.ExecContext(ctx, `UPDATE ingest_cursors SET cursor=$2, last_status=$3, last_error=$4,
		last_polled_at=now(), updated_at=now(), leader=$5,
		consecutive_errors = CASE WHEN $6 THEN consecutive_errors + 1 ELSE 0 END
		WHERE name=$1`, CursorName, cursor, status, errMsg, p.Instance, failed)
	if err != nil && ctx.Err() == nil {
		slog.Error("record poll state", "err", err)
	}
}
