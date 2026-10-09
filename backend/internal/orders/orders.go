// Package orders holds the transactional operations on orders: creating
// them, moving them through the lifecycle and recording history.
package orders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/jj0629/lab37-homework/backend/internal/normalize"
)

// Q is satisfied by *sql.DB, *sql.Tx and *sql.Conn.
type Q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ErrNotFound is returned when an order does not exist.
var ErrNotFound = errors.New("order not found")

// AddEvent appends a history entry. payload may be nil, raw JSON or any value.
func AddEvent(ctx context.Context, q Q, orderID int64, typ, from, to, msg string, payload any) error {
	var p any
	switch v := payload.(type) {
	case nil:
	case json.RawMessage:
		p = []byte(v)
	case []byte:
		p = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		p = b
	}
	_, err := q.ExecContext(ctx, `INSERT INTO order_events (order_id, type, from_status, to_status, message, payload)
		VALUES ($1, $2, $3, $4, $5, $6)`, orderID, typ, from, to, msg, p)
	return err
}

// Insert creates an order and its items in the given status. If an order
// with the same (source, external_id) already exists, created is false and
// id is 0; the caller decides whether that is a duplicate or a conflict.
func Insert(ctx context.Context, tx Q, o normalize.Order, status string) (id int64, created bool, err error) {
	if o.Flags == nil {
		o.Flags = []string{}
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO orders
		(source, external_id, channel, restaurant, customer_first, customer_last, total, notes,
		 status, kind, meal, scheduled_for, flags, needs_review)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (source, external_id) DO NOTHING
		RETURNING id`,
		o.Source, o.ExternalID, o.Channel, o.Restaurant, o.First, o.Last, o.Total, o.Notes,
		status, o.Kind, o.Meal, o.ScheduledFor, pq.Array(o.Flags), o.NeedsReview,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	for _, it := range o.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, item_key, name, category, price, qty, external_status)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, it.Key, it.Name, it.Category, it.Price, it.Qty, it.ExternalStatus); err != nil {
			return 0, false, err
		}
	}
	return id, true, nil
}

// Locked is the minimal order state read under a row lock.
type Locked struct {
	ID             int64
	Status         string
	ExternalStatus string
}

// FindForUpdate locks an order by its source key.
func FindForUpdate(ctx context.Context, tx Q, source, externalID string) (Locked, error) {
	var l Locked
	err := tx.QueryRowContext(ctx, `SELECT id, status, external_status FROM orders
		WHERE source=$1 AND external_id=$2 FOR UPDATE`, source, externalID).Scan(&l.ID, &l.Status, &l.ExternalStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// LockByID locks an order by primary key.
func LockByID(ctx context.Context, tx Q, id int64) (Locked, error) {
	var l Locked
	err := tx.QueryRowContext(ctx, `SELECT id, status, external_status FROM orders WHERE id=$1 FOR UPDATE`, id).
		Scan(&l.ID, &l.Status, &l.ExternalStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// Transition moves an order from one status to another and records why.
// It is a no-op (returning false) if the move would not be forward.
func Transition(ctx context.Context, tx Q, id int64, from, to, eventType, msg string, payload any) (bool, error) {
	if !normalize.IsForward(from, to) {
		return false, nil
	}
	extra := ""
	if to == normalize.StatusDispatched {
		extra = ", dispatched_at = now()"
	}
	res, err := tx.ExecContext(ctx, `UPDATE orders SET status=$1, updated_at=now()`+extra+` WHERE id=$2 AND status=$3`, to, id, from)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, AddEvent(ctx, tx, id, eventType, from, to, msg, payload)
}

// AddFlag adds a flag (idempotently) and optionally marks the order for review.
func AddFlag(ctx context.Context, tx Q, id int64, flag string, review bool) error {
	_, err := tx.ExecContext(ctx, `UPDATE orders SET
		flags = CASE WHEN $2 = ANY(flags) THEN flags ELSE array_append(flags, $2) END,
		needs_review = needs_review OR $3, updated_at = now()
		WHERE id=$1`, id, flag, review)
	return err
}

// CancelResult describes what a cancellation did.
type CancelResult string

const (
	CancelDone      CancelResult = "cancelled"        // stopped before dispatch
	CancelRequested CancelResult = "cancel_requested" // already with the robot; cancel message queued
	CancelIgnored   CancelResult = "ignored"          // already finished
)

// Cancel cancels a locked order. Before dispatch the order simply stops.
// After dispatch we cannot un-cook food, so we record the request and queue
// a cancel message to the robot; a human or the robot decides what happens.
func Cancel(ctx context.Context, tx Q, l Locked, reason string, payload any) (CancelResult, error) {
	switch {
	case normalize.CancellableBeforeDispatch(l.Status):
		_, err := Transition(ctx, tx, l.ID, l.Status, normalize.StatusCancelled, "cancelled", reason, payload)
		return CancelDone, err
	case l.Status == normalize.StatusDispatched || l.Status == normalize.StatusOutForDelivery:
		if err := AddEvent(ctx, tx, l.ID, "cancel_requested", l.Status, l.Status, reason+" (already dispatched; cancel sent to robot)", payload); err != nil {
			return "", err
		}
		if err := AddFlag(ctx, tx, l.ID, "cancel_after_dispatch", true); err != nil {
			return "", err
		}
		msg := map[string]any{"order_id": l.ID, "dispatch_id": DispatchID(l.ID), "action": "cancel", "reason": reason,
			"idempotency_key": fmt.Sprintf("cancel:%d", l.ID), "created_at": time.Now().UTC()}
		b, _ := json.Marshal(msg)
		_, err := tx.ExecContext(ctx, `INSERT INTO dispatch_outbox (order_id, kind, idem_key, payload)
			VALUES ($1, 'cancel', $2, $3) ON CONFLICT (idem_key) DO NOTHING`, l.ID, fmt.Sprintf("cancel:%d", l.ID), b)
		return CancelRequested, err
	default:
		return CancelIgnored, AddEvent(ctx, tx, l.ID, "cancel_ignored", l.Status, l.Status, reason+" (order already "+l.Status+")", payload)
	}
}

// DispatchID is the robot-facing id for an order's dispatch.
func DispatchID(orderID int64) string { return fmt.Sprintf("D-%07d", orderID) }

// RobotItem is one line in the robot payload.
type RobotItem struct {
	Name     string `json:"name"`
	Qty      int    `json:"qty"`
	Category string `json:"category,omitempty"`
}

// RobotPayload is the skeleton message sent to the assembly robot.
type RobotPayload struct {
	DispatchID     string      `json:"dispatch_id"`
	OrderID        int64       `json:"order_id"`
	Source         string      `json:"source"`
	ExternalID     string      `json:"external_id"`
	Restaurant     string      `json:"restaurant,omitempty"`
	Customer       Customer    `json:"customer"`
	Items          []RobotItem `json:"items"`
	Notes          string      `json:"notes,omitempty"`
	Flags          []string    `json:"flags"`
	ScheduledFor   *time.Time  `json:"scheduled_for,omitempty"`
	IdempotencyKey string      `json:"idempotency_key"`
	CreatedAt      time.Time   `json:"created_at"`
}

// Customer identifies who the food is for.
type Customer struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// BuildRobotPayload assembles the robot message from the current order state.
func BuildRobotPayload(ctx context.Context, q Q, id int64) (RobotPayload, error) {
	p := RobotPayload{OrderID: id, DispatchID: DispatchID(id), IdempotencyKey: fmt.Sprintf("dispatch:%d", id), CreatedAt: time.Now().UTC()}
	var flags pq.StringArray
	var sched sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT source, external_id, restaurant, customer_first, customer_last, notes, flags, scheduled_for
		FROM orders WHERE id=$1`, id).Scan(&p.Source, &p.ExternalID, &p.Restaurant, &p.Customer.First, &p.Customer.Last, &p.Notes, &flags, &sched)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Flags = []string(flags)
	if sched.Valid {
		t := sched.Time
		p.ScheduledFor = &t
	}
	rows, err := q.QueryContext(ctx, `SELECT name, qty, category FROM order_items
		WHERE order_id=$1 AND external_status <> 'cancelled' ORDER BY id`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Items = []RobotItem{}
	for rows.Next() {
		var it RobotItem
		if err := rows.Scan(&it.Name, &it.Qty, &it.Category); err != nil {
			return p, err
		}
		p.Items = append(p.Items, it)
	}
	return p, rows.Err()
}

// DeadLetter records a payload that needs a human.
func DeadLetter(ctx context.Context, q Q, source, externalID, reason, detail string, payload []byte, inboxID, orderID *int64) error {
	var p any
	if len(payload) > 0 {
		p = payload
	}
	_, err := q.ExecContext(ctx, `INSERT INTO dead_letter (source, external_id, reason, detail, payload, inbox_id, order_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (inbox_id) WHERE inbox_id IS NOT NULL DO NOTHING`,
		source, externalID, reason, detail, p, inboxID, orderID)
	return err
}
