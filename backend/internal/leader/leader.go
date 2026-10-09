// Package leader provides single-active-instance roles using Postgres
// session advisory locks. Any number of replicas can run a role; exactly one
// holds the lock and does the work. If that process dies, Postgres drops its
// session and the lock, and another replica takes over within a few seconds.
package leader

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Role keys for pg_try_advisory_lock.
const (
	KeyPoller    int64 = 7_370_101
	KeyScheduler int64 = 7_370_102
)

// Run competes for the lock and runs fn while it is held. fn must return
// when its context is cancelled. Run blocks until ctx is cancelled.
func Run(ctx context.Context, db *sql.DB, role string, key int64, instance string, fn func(ctx context.Context)) {
	for ctx.Err() == nil {
		held := hold(ctx, db, role, key, instance, fn)
		wait := 2 * time.Second
		if held {
			wait = 500 * time.Millisecond // lost leadership: re-compete quickly
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// hold tries once to take the lock; if successful it runs fn until the lock
// connection fails or ctx ends. Returns whether leadership was obtained.
func hold(ctx context.Context, db *sql.DB, role string, key int64, instance string, fn func(ctx context.Context)) bool {
	conn, err := db.Conn(ctx)
	if err != nil {
		return false
	}
	defer conn.Close()

	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil || !ok {
		return false
	}
	slog.Info("became leader", "role", role)
	beat := func() error {
		_, err := conn.ExecContext(ctx, `INSERT INTO leaders (role, holder) VALUES ($1, $2)
			ON CONFLICT (role) DO UPDATE SET holder=EXCLUDED.holder, heartbeat=now(),
			since = CASE WHEN leaders.holder = EXCLUDED.holder THEN leaders.since ELSE now() END`, role, instance)
		return err
	}
	_ = beat()

	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); fn(lctx) }()

	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
			slog.Info("released leadership", "role", role)
			return true
		case <-t.C:
			// The lock lives on this session; if the session is gone, so is the lock.
			if err := beat(); err != nil {
				slog.Warn("lost leadership", "role", role, "err", err)
				cancel()
				<-done
				return true
			}
		}
	}
}
