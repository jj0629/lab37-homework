// Package dispatch hands queued orders to the robot.
//
// Two loops, both safe to run on every worker replica:
//   - Dispatcher: queued → dispatched. In one transaction it builds the robot
//     payload, writes it to dispatch_outbox and updates the order.
//   - Sender: delivers outbox rows to the robot over HTTP with an
//     idempotency key, retrying with backoff. The robot may see a message
//     more than once, never zero times.
package dispatch

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/jj0629/lab37-homework/backend/internal/normalize"
	"github.com/jj0629/lab37-homework/backend/internal/orders"
)

// Dispatcher moves queued orders to dispatched via the outbox.
type Dispatcher struct {
	DB *sql.DB
}

// Run loops until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		n, err := d.Batch(ctx, 25)
		if err != nil && ctx.Err() == nil {
			slog.Error("dispatcher", "err", err)
		}
		if n > 0 {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(250*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond):
		}
	}
}

// Batch dispatches up to limit queued orders in one transaction.
func (d *Dispatcher) Batch(ctx context.Context, limit int) (int, error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx, `SELECT id FROM orders WHERE status='queued'
		ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}
	for _, id := range ids {
		p, err := orders.BuildRobotPayload(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		b, _ := json.Marshal(p)
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch_outbox (order_id, kind, idem_key, payload)
			VALUES ($1, 'dispatch', $2, $3) ON CONFLICT (idem_key) DO NOTHING`, id, p.IdempotencyKey, b); err != nil {
			return 0, err
		}
		if _, err := orders.Transition(ctx, tx, id, normalize.StatusQueued, normalize.StatusDispatched, "dispatched",
			"sent to robot as "+p.DispatchID, json.RawMessage(b)); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit()
}

// Sender delivers outbox messages to the robot endpoint.
type Sender struct {
	DB          *sql.DB
	RobotURL    string
	MaxAttempts int
	Client      *http.Client
}

// Run loops until ctx is cancelled.
func (s *Sender) Run(ctx context.Context) {
	if s.Client == nil {
		s.Client = &http.Client{Timeout: 5 * time.Second}
	}
	for ctx.Err() == nil {
		worked, err := s.sendOne(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("outbox sender", "err", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(250*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond):
		}
	}
}

func (s *Sender) sendOne(ctx context.Context) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	var id, orderID int64
	var kind, key string
	var payload []byte
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT id, order_id, kind, idem_key, payload, attempts FROM dispatch_outbox
		WHERE status='pending' AND next_attempt_at <= now() ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&id, &orderID, &kind, &key, &payload, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	url := s.RobotURL
	if kind == "cancel" {
		url = s.RobotURL + "/cancel"
	}
	ack, sendErr := s.post(ctx, url, key, payload)
	attempts++
	if sendErr == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE dispatch_outbox SET status='sent', sent_at=now(), attempts=$2, last_error='' WHERE id=$1`, id, attempts); err != nil {
			return true, err
		}
		typ := "robot_acknowledged"
		if kind == "cancel" {
			typ = "robot_cancel_acknowledged"
		}
		if err := orders.AddEvent(ctx, tx, orderID, typ, "", "", fmt.Sprintf("robot accepted %s message (attempt %d)", kind, attempts), ack); err != nil {
			return true, err
		}
		return true, tx.Commit()
	}

	if attempts >= s.MaxAttempts {
		slog.Warn("robot delivery failed permanently", "outbox", id, "order", orderID, "err", sendErr)
		if _, err := tx.ExecContext(ctx, `UPDATE dispatch_outbox SET status='failed', attempts=$2, last_error=$3 WHERE id=$1`, id, attempts, sendErr.Error()); err != nil {
			return true, err
		}
		if err := orders.AddFlag(ctx, tx, orderID, "robot_delivery_failed", true); err != nil {
			return true, err
		}
		if err := orders.AddEvent(ctx, tx, orderID, "robot_delivery_failed", "", "", sendErr.Error(), nil); err != nil {
			return true, err
		}
		return true, tx.Commit()
	}
	backoff := time.Duration(1<<min(attempts, 6)) * 500 * time.Millisecond
	if _, err := tx.ExecContext(ctx, `UPDATE dispatch_outbox SET attempts=$2, last_error=$3,
		next_attempt_at = now() + $4 * interval '1 millisecond' WHERE id=$1`, id, attempts, sendErr.Error(), backoff.Milliseconds()); err != nil {
		return true, err
	}
	return true, tx.Commit()
}

func (s *Sender) post(ctx context.Context, url, key string, payload []byte) (json.RawMessage, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("robot HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	if !json.Valid(body) {
		body, _ = json.Marshal(map[string]string{"raw": string(body)})
	}
	return body, nil
}
