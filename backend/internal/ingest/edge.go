// Package ingest is the write path: the edge that accepts payloads into the
// inbox, and the workers that turn inbox rows into orders.
package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jj0629/lab37-homework/backend/internal/normalize"
	"github.com/jj0629/lab37-homework/backend/internal/store"
)

// Enqueued reports what happened to one payload at the edge.
type Enqueued struct {
	InboxID   int64 `json:"inbox_id"`
	Duplicate bool  `json:"duplicate"`
}

// Enqueue durably records a payload for asynchronous processing.
// The first delivery of an idempotency key is a normal row; any repeat is
// recorded as a duplicate row so it shows up in the order's history but is
// never applied twice.
func Enqueue(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, source, externalID string, payload []byte, uploadID *int64) (Enqueued, error) {
	key := normalize.IdemKey(source, externalID, normalize.PayloadHash(payload))
	var e Enqueued
	err := q.QueryRowContext(ctx, `INSERT INTO inbox (source, external_id, idem_key, payload, upload_id)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (idem_key) WHERE NOT is_duplicate DO NOTHING
		RETURNING id`, source, externalID, key, payload, uploadID).Scan(&e.InboxID)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return e, err
	}
	e.Duplicate = true
	err = q.QueryRowContext(ctx, `INSERT INTO inbox (source, external_id, idem_key, payload, upload_id, is_duplicate)
		VALUES ($1,$2,$3,$4,$5,true) RETURNING id`, source, externalID, key, payload, uploadID).Scan(&e.InboxID)
	return e, err
}

// EnqueueWebhook validates the minimum needed to key a webhook payload and enqueues it.
func EnqueueWebhook(ctx context.Context, db *sql.DB, raw []byte) (Enqueued, error) {
	p, err := normalize.ParseWebhook(raw)
	if err != nil {
		return Enqueued{}, err
	}
	return Enqueue(ctx, db, "webhook", p.OrderID, raw, nil)
}

// ImportResult summarises a CSV upload.
type ImportResult struct {
	UploadID  int64                 `json:"upload_id"`
	Filename  string                `json:"filename"`
	Rows      int                   `json:"rows"`
	New       int                   `json:"new"`
	Duplicate int                   `json:"duplicate"`
	Rejected  []normalize.Rejection `json:"rejected"`
}

// ImportCSV parses a survey export and enqueues every valid row in one
// transaction, so an upload is all-or-nothing.
func ImportCSV(ctx context.Context, db *sql.DB, filename string, r io.Reader, sha string, meals map[string]string) (ImportResult, error) {
	res := ImportResult{Filename: filename, Rejected: []normalize.Rejection{}}
	rows, rejected, err := normalize.ParseCSV(r, meals)
	if err != nil {
		return res, err
	}
	if rejected != nil {
		res.Rejected = rejected
	}
	res.Rows = len(rows) + len(rejected)
	err = store.InTx(ctx, db, func(tx *sql.Tx) error {
		rej, _ := json.Marshal(res.Rejected)
		if err := tx.QueryRowContext(ctx, `INSERT INTO csv_uploads (filename, sha256, row_count, new_count, dup_count, rejected)
			VALUES ($1,$2,$3,0,0,$4) RETURNING id`, filename, sha, res.Rows, rej).Scan(&res.UploadID); err != nil {
			return err
		}
		for _, row := range rows {
			b, err := json.Marshal(row)
			if err != nil {
				return err
			}
			e, err := Enqueue(ctx, tx, "csv", row.ExternalID, b, &res.UploadID)
			if err != nil {
				return fmt.Errorf("line %d: %w", row.Line, err)
			}
			if e.Duplicate {
				res.Duplicate++
			} else {
				res.New++
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE csv_uploads SET new_count=$1, dup_count=$2 WHERE id=$3`, res.New, res.Duplicate, res.UploadID)
		return err
	})
	return res, err
}
