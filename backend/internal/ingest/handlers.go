package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jj0629/lab37-homework/backend/internal/normalize"
	"github.com/jj0629/lab37-homework/backend/internal/orders"
	"github.com/jj0629/lab37-homework/backend/internal/store"
)

// handleWebhook applies one webhook delivery: a new order, a cancellation,
// a duplicate, or an id conflict.
func (w *Worker) handleWebhook(ctx context.Context, tx *sql.Tx, r inboxRow) error {
	if r.IsDuplicate {
		return recordDuplicate(ctx, tx, r)
	}
	p, err := normalize.ParseWebhook(r.Payload)
	if err != nil {
		return permanent("invalid_payload", err)
	}

	if p.IsCancel() {
		l, err := orders.FindForUpdate(ctx, tx, "webhook", p.OrderID)
		if errors.Is(err, orders.ErrNotFound) {
			// The cancel may have overtaken its order; retry with backoff and
			// dead-letter if the order never shows up.
			return fmt.Errorf("cancel for unknown order %s", p.OrderID)
		}
		if err != nil {
			return err
		}
		_, err = orders.Cancel(ctx, tx, l, "cancelled by customer via "+p.OrderSource, json.RawMessage(r.Payload))
		return err
	}

	o := normalize.WebhookOrder(p)
	if reason := normalize.RejectReason(o); reason != "" {
		id, created, err := orders.Insert(ctx, tx, o, normalize.StatusRejected)
		if err != nil {
			return err
		}
		if !created {
			return w.conflict(ctx, tx, r, p.OrderID)
		}
		if err := orders.AddEvent(ctx, tx, id, "received", "", normalize.StatusReceived, "webhook from "+p.OrderSource, json.RawMessage(r.Payload)); err != nil {
			return err
		}
		if err := orders.AddEvent(ctx, tx, id, "rejected", normalize.StatusReceived, normalize.StatusRejected, reason, nil); err != nil {
			return err
		}
		return orders.DeadLetter(ctx, tx, "webhook", p.OrderID, reason, "order rejected: "+reason, r.Payload, &r.ID, &id)
	}

	id, created, err := orders.Insert(ctx, tx, o, normalize.StatusReceived)
	if err != nil {
		return err
	}
	if !created {
		return w.conflict(ctx, tx, r, p.OrderID)
	}
	if err := orders.AddEvent(ctx, tx, id, "received", "", normalize.StatusReceived, "webhook from "+p.OrderSource, json.RawMessage(r.Payload)); err != nil {
		return err
	}
	_, err = orders.Transition(ctx, tx, id, normalize.StatusReceived, normalize.StatusQueued, "queued", "real-time order, ready for robot", nil)
	return err
}

// conflict quarantines a payload that reuses an existing order id with
// different content. The first order wins; the newcomer goes to review.
func (w *Worker) conflict(ctx context.Context, tx *sql.Tx, r inboxRow, externalID string) error {
	l, err := orders.FindForUpdate(ctx, tx, r.Source, externalID)
	if err != nil {
		return err
	}
	if err := orders.DeadLetter(ctx, tx, r.Source, externalID, "id_conflict",
		"a different order already uses this id; kept the first one", r.Payload, &r.ID, &l.ID); err != nil {
		return err
	}
	if err := orders.AddFlag(ctx, tx, l.ID, "id_conflict", true); err != nil {
		return err
	}
	return orders.AddEvent(ctx, tx, l.ID, "conflict_quarantined", l.Status, l.Status,
		"another payload arrived with this order id and different contents; quarantined for review", json.RawMessage(r.Payload))
}

// handleCSV turns a survey row into a scheduled order.
func (w *Worker) handleCSV(ctx context.Context, tx *sql.Tx, r inboxRow) error {
	if r.IsDuplicate {
		return recordDuplicate(ctx, tx, r)
	}
	var row normalize.CSVRow
	if err := json.Unmarshal(r.Payload, &row); err != nil {
		return permanent("invalid_payload", err)
	}
	now, err := store.Now(ctx, tx)
	if err != nil {
		return err
	}
	o := normalize.CSVOrder(row, now, w.Cfg.SlotTZ, w.Cfg.MealTimes)
	id, created, err := orders.Insert(ctx, tx, o, normalize.StatusReceived)
	if err != nil {
		return err
	}
	if !created {
		return nil // same row key already applied (can only happen on replays)
	}
	msg := "survey CSV row"
	if r.UploadID.Valid {
		msg = fmt.Sprintf("survey CSV upload #%d, line %d", r.UploadID.Int64, row.Line)
	}
	if err := orders.AddEvent(ctx, tx, id, "received", "", normalize.StatusReceived, msg, json.RawMessage(r.Payload)); err != nil {
		return err
	}
	when := o.ScheduledFor.In(w.Cfg.SlotTZ).Format("Mon Jan 2 15:04 MST")
	note := fmt.Sprintf("%s on %s; robot release %d min before", o.Meal, when, w.Cfg.LeadMinutes)
	for _, f := range o.Flags {
		if f == "slot_passed_rescheduled" {
			note = "today's " + o.Meal + " slot had passed; moved to " + when
		}
	}
	_, err = orders.Transition(ctx, tx, id, normalize.StatusReceived, normalize.StatusScheduled, "scheduled", note, nil)
	return err
}

// handleAPI applies one order's item deltas from the polling API. Deltas are
// idempotent and monotonic: item statuses only move forward, so replays
// (e.g. after a 500 with partial data) and out-of-order arrival are safe.
func (w *Worker) handleAPI(ctx context.Context, tx *sql.Tx, r inboxRow) error {
	if r.IsDuplicate {
		return recordDuplicate(ctx, tx, r)
	}
	var d normalize.APIDelta
	if err := json.Unmarshal(r.Payload, &d); err != nil {
		return permanent("invalid_payload", err)
	}
	ext := d.ExternalID()

	l, err := orders.FindForUpdate(ctx, tx, "api", ext)
	if errors.Is(err, orders.ErrNotFound) {
		o := normalize.Order{Source: "api", ExternalID: ext, Channel: "poll-api", Restaurant: "", Kind: "realtime", Flags: []string{}}
		id, created, err := orders.Insert(ctx, tx, o, normalize.StatusReceived)
		if err != nil {
			return err
		}
		if created {
			if err := orders.AddEvent(ctx, tx, id, "received", "", normalize.StatusReceived, fmt.Sprintf("first seen in order API (order #%d)", d.Order), nil); err != nil {
				return err
			}
			if _, err := orders.Transition(ctx, tx, id, normalize.StatusReceived, normalize.StatusQueued, "queued", "dispatch on first sight", nil); err != nil {
				return err
			}
		}
		if l, err = orders.FindForUpdate(ctx, tx, "api", ext); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Current items, read under the order lock.
	type cur struct{ status string }
	existing := map[string]cur{}
	rows, err := tx.QueryContext(ctx, `SELECT item_key, external_status FROM order_items WHERE order_id=$1`, l.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, s string
		if err := rows.Scan(&k, &s); err != nil {
			rows.Close()
			return err
		}
		existing[k] = cur{s}
	}
	rows.Close()

	keys := make([]string, 0, len(d.Items))
	for k := range d.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var added []string
	var advanced []map[string]string
	for _, k := range keys {
		it := d.Items[k]
		price := it.Price
		if c, ok := existing[k]; ok {
			next := normalize.LaterExternal(c.status, it.Status)
			if next != c.status {
				if _, err := tx.ExecContext(ctx, `UPDATE order_items SET external_status=$1, price=$2 WHERE order_id=$3 AND item_key=$4`, next, price, l.ID, k); err != nil {
					return err
				}
				advanced = append(advanced, map[string]string{"item": it.Name, "from": c.status, "to": next})
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, item_key, name, category, price, qty, external_status)
			VALUES ($1,$2,$3,$4,$5,1,$6)`, l.ID, k, it.Name, it.Category, price, it.Status); err != nil {
			return err
		}
		added = append(added, it.Name)
	}
	if len(added) > 0 {
		if err := orders.AddEvent(ctx, tx, l.ID, "items_added", l.Status, l.Status, fmt.Sprintf("%d item(s) added", len(added)), map[string]any{"items": added}); err != nil {
			return err
		}
	}
	if len(advanced) > 0 {
		if err := orders.AddEvent(ctx, tx, l.ID, "items_updated", l.Status, l.Status, fmt.Sprintf("%d item status change(s)", len(advanced)), advanced); err != nil {
			return err
		}
	}

	// Recompute order-level total and upstream status from all items.
	var statuses []string
	srows, err := tx.QueryContext(ctx, `SELECT external_status FROM order_items WHERE order_id=$1`, l.ID)
	if err != nil {
		return err
	}
	for srows.Next() {
		var s string
		if err := srows.Scan(&s); err != nil {
			srows.Close()
			return err
		}
		statuses = append(statuses, s)
	}
	srows.Close()
	agg := normalize.AggregateExternal(statuses)
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET external_status=$1, updated_at=now(),
		total=(SELECT sum(price*qty) FROM order_items WHERE order_id=$2 AND external_status <> 'cancelled') WHERE id=$2`, agg, l.ID); err != nil {
		return err
	}
	if agg != l.ExternalStatus {
		if err := orders.AddEvent(ctx, tx, l.ID, "external_status_changed", l.Status, l.Status,
			fmt.Sprintf("upstream status %s → %s", orEmpty(l.ExternalStatus), agg), nil); err != nil {
			return err
		}
		if to := normalize.MapExternal(agg); to != "" {
			if _, err := orders.Transition(ctx, tx, l.ID, l.Status, to, "status_changed", "upstream reports "+agg, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func orEmpty(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
