package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jj0629/lab37-homework/backend/internal/config"
	"github.com/jj0629/lab37-homework/backend/internal/orders"
)

// inboxRow is a claimed unit of work.
type inboxRow struct {
	ID          int64
	Source      string
	ExternalID  string
	IsDuplicate bool
	Payload     []byte
	Attempts    int
	UploadID    sql.NullInt64
	ReceivedAt  time.Time
}

// permanentError marks failures that retrying cannot fix (bad payloads).
type permanentError struct {
	reason string
	err    error
}

func (e permanentError) Error() string { return e.reason + ": " + e.err.Error() }

func permanent(reason string, err error) error { return permanentError{reason, err} }

// Worker drains the inbox. Many workers (goroutines and processes) can run
// at once: each claims one row with FOR UPDATE SKIP LOCKED and processes it
// inside the same transaction, so a row is applied exactly once and a crash
// mid-way simply rolls back and the row becomes claimable again.
type Worker struct {
	DB  *sql.DB
	Cfg config.Config
}

// Run starts n claim loops and blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context, n int) {
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			w.loop(ctx, i)
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

func (w *Worker) loop(ctx context.Context, idx int) {
	idle := 100 * time.Millisecond
	for ctx.Err() == nil {
		worked, err := w.processOne(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("inbox worker", "worker", idx, "err", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(idle + time.Duration(rand.IntN(100))*time.Millisecond):
		}
	}
}

// processOne claims and processes a single inbox row. It returns false when
// there was nothing to do.
func (w *Worker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	var r inboxRow
	err = tx.QueryRowContext(ctx, `SELECT id, source, external_id, is_duplicate, payload, attempts, upload_id, received_at
		FROM inbox WHERE status='pending' AND next_attempt_at <= now()
		ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&r.ID, &r.Source, &r.ExternalID, &r.IsDuplicate, &r.Payload, &r.Attempts, &r.UploadID, &r.ReceivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Savepoint so a handler failure can be rolled back while we still hold
	// the row lock and record the failure in the same transaction.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT handler`); err != nil {
		return true, err
	}
	herr := w.handle(ctx, tx, r)
	if herr == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET status='done', processed_at=now(), attempts=attempts+1, last_error='' WHERE id=$1`, r.ID); err != nil {
			return true, err
		}
		return true, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT handler`); err != nil {
		return true, err
	}
	if err := w.recordFailure(ctx, tx, r, herr); err != nil {
		return true, err
	}
	return true, tx.Commit()
}

// recordFailure schedules a retry with exponential backoff, or dead-letters
// the row when the error is permanent or attempts are exhausted.
func (w *Worker) recordFailure(ctx context.Context, tx *sql.Tx, r inboxRow, herr error) error {
	attempts := r.Attempts + 1
	var perm permanentError
	isPerm := errors.As(herr, &perm)
	if isPerm || attempts >= w.Cfg.MaxAttempts {
		reason := "max_attempts_exceeded"
		if isPerm {
			reason = perm.reason
		}
		slog.Warn("dead-lettering inbox row", "id", r.ID, "source", r.Source, "reason", reason, "err", herr)
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET status='dead', attempts=$2, last_error=$3, processed_at=now() WHERE id=$1`,
			r.ID, attempts, herr.Error()); err != nil {
			return err
		}
		return orders.DeadLetter(ctx, tx, r.Source, r.ExternalID, reason, herr.Error(), r.Payload, &r.ID, nil)
	}
	backoff := time.Duration(1<<min(attempts, 8)) * 500 * time.Millisecond
	backoff += time.Duration(rand.IntN(250)) * time.Millisecond
	slog.Info("inbox retry scheduled", "id", r.ID, "attempt", attempts, "in", backoff.String(), "err", herr)
	_, err := tx.ExecContext(ctx, `UPDATE inbox SET attempts=$2, last_error=$3, next_attempt_at=now() + $4 * interval '1 millisecond' WHERE id=$1`,
		r.ID, attempts, herr.Error(), backoff.Milliseconds())
	return err
}

func (w *Worker) handle(ctx context.Context, tx *sql.Tx, r inboxRow) error {
	switch r.Source {
	case "webhook":
		return w.handleWebhook(ctx, tx, r)
	case "csv":
		return w.handleCSV(ctx, tx, r)
	case "api":
		return w.handleAPI(ctx, tx, r)
	}
	return permanent("unknown_source", fmt.Errorf("source %q", r.Source))
}

// recordDuplicate notes a repeated delivery on the order it duplicates.
// If the original has not been processed yet we retry shortly.
func recordDuplicate(ctx context.Context, tx *sql.Tx, r inboxRow) error {
	l, err := orders.FindForUpdate(ctx, tx, r.Source, r.ExternalID)
	if errors.Is(err, orders.ErrNotFound) {
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inbox WHERE source=$1 AND external_id=$2 AND NOT is_duplicate AND status='pending')`,
			r.Source, r.ExternalID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return fmt.Errorf("original delivery not processed yet")
		}
		return nil // original was dead-lettered; nothing to attach to
	}
	if err != nil {
		return err
	}
	detail := map[string]any{"inbox_id": r.ID, "received_at": r.ReceivedAt}
	msg := "identical payload received again; ignored"
	if r.UploadID.Valid {
		detail["upload_id"] = r.UploadID.Int64
		msg = fmt.Sprintf("row seen again in CSV upload #%d; ignored", r.UploadID.Int64)
	}
	return orders.AddEvent(ctx, tx, l.ID, "duplicate_received", l.Status, l.Status, msg, detail)
}
