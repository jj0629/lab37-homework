// Package scheduler releases scheduled orders to the dispatch queue when
// their meal slot is within the lead time (leader only).
package scheduler

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Scheduler moves scheduled → queued when app_now() >= slot - lead.
type Scheduler struct {
	DB          *sql.DB
	LeadMinutes int
	Every       time.Duration
}

// Run ticks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	if s.Every == 0 {
		s.Every = 2 * time.Second
	}
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		if n, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Error("scheduler tick", "err", err)
		} else if n > 0 {
			slog.Info("released scheduled orders", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick releases every due order in one statement and returns how many.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
		WITH due AS (
			SELECT id FROM orders
			WHERE status = 'scheduled' AND scheduled_for - make_interval(mins => $1::int) <= app_now()
			ORDER BY scheduled_for, id LIMIT 1000
			FOR UPDATE SKIP LOCKED
		), upd AS (
			UPDATE orders o SET status = 'queued', updated_at = now()
			FROM due WHERE o.id = due.id
			RETURNING o.id
		), ev AS (
			INSERT INTO order_events (order_id, type, from_status, to_status, message)
			SELECT id, 'released', 'scheduled', 'queued', 'meal slot is within ' || $1::int::text || ' min; sent to dispatch queue' FROM upd
		)
		SELECT count(*) FROM upd`, s.LeadMinutes).Scan(&n)
	return n, err
}
