// Package normalize turns raw payloads from each source into one canonical
// order shape. Everything here is pure (no I/O) so it is easy to unit test.
package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Order lifecycle states.
const (
	StatusReceived       = "received"
	StatusScheduled      = "scheduled"
	StatusQueued         = "queued"
	StatusDispatched     = "dispatched"
	StatusOutForDelivery = "out_for_delivery"
	StatusCompleted      = "completed"
	StatusCancelled      = "cancelled"
	StatusRejected       = "rejected"
)

// statusRank orders the forward path; statuses only ever move to a higher rank.
var statusRank = map[string]int{
	StatusReceived:       0,
	StatusScheduled:      1,
	StatusQueued:         2,
	StatusDispatched:     3,
	StatusOutForDelivery: 4,
	StatusCompleted:      5,
}

// IsTerminal reports whether no further transitions are allowed.
func IsTerminal(s string) bool {
	return s == StatusCompleted || s == StatusCancelled || s == StatusRejected
}

// IsForward reports whether moving from -> to advances the lifecycle.
func IsForward(from, to string) bool {
	if IsTerminal(from) {
		return false
	}
	if to == StatusCancelled || to == StatusRejected {
		return true
	}
	fr, ok1 := statusRank[from]
	tr, ok2 := statusRank[to]
	return ok1 && ok2 && tr > fr
}

// CancellableBeforeDispatch reports whether a cancel can simply stop the order.
func CancellableBeforeDispatch(s string) bool {
	return s == StatusReceived || s == StatusScheduled || s == StatusQueued
}

// Item is one line on an order.
type Item struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Category       string   `json:"category,omitempty"`
	Price          *float64 `json:"price,omitempty"`
	Qty            int      `json:"qty"`
	ExternalStatus string   `json:"external_status,omitempty"`
}

// Order is the canonical, source-independent order.
type Order struct {
	Source       string     `json:"source"`
	ExternalID   string     `json:"external_id"`
	Channel      string     `json:"channel"`
	Restaurant   string     `json:"restaurant"`
	First        string     `json:"first_name"`
	Last         string     `json:"last_name"`
	Total        *float64   `json:"total,omitempty"`
	Notes        string     `json:"notes"`
	Kind         string     `json:"kind"` // realtime | scheduled
	Meal         string     `json:"meal,omitempty"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
	Flags        []string   `json:"flags"`
	NeedsReview  bool       `json:"needs_review"`
	Items        []Item     `json:"items"`
}

// PayloadHash returns a stable hash of a JSON document. Keys are re-ordered
// by re-encoding, so semantically equal payloads hash the same.
func PayloadHash(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if canon, err := json.Marshal(v); err == nil {
			raw = canon
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// IdemKey is the inbox idempotency key: same source, id and content = same delivery.
func IdemKey(source, externalID, payloadHash string) string {
	return source + ":" + externalID + ":" + payloadHash[:16]
}

// NoteFlags inspects free-text notes. Item-change requests and change-of-mind
// notes need a human look but never block dispatch; allergies are surfaced
// to the robot as a flag.
func NoteFlags(notes string) (flags []string, needsReview bool) {
	n := strings.ToLower(strings.TrimSpace(notes))
	switch {
	case strings.HasPrefix(n, "i know i added"):
		flags = append(flags, "item_change_requested")
		needsReview = true
	case strings.HasPrefix(n, "changed my mind"):
		flags = append(flags, "instructions_changed")
		needsReview = true
	}
	if strings.Contains(n, "allerg") {
		flags = append(flags, "allergy")
	}
	return flags, needsReview
}
