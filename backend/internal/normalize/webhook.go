package normalize

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// WebhookPayload is one line of webhook_orders.jsonl.
type WebhookPayload struct {
	OrderID     string   `json:"order_id"`
	OrderSource string   `json:"order_source"`
	Restaurant  string   `json:"restaurant"`
	FirstName   string   `json:"first_name"`
	LastName    string   `json:"last_name"`
	Total       *float64 `json:"total"`
	Items       []string `json:"items"`
	Notes       string   `json:"notes"`
	Update      []string `json:"update,omitempty"`
}

// ErrMissingID is returned for payloads we cannot even key.
var ErrMissingID = errors.New("missing order_id")

// ParseWebhook decodes and minimally validates a webhook payload.
func ParseWebhook(raw []byte) (WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("invalid json: %w", err)
	}
	p.OrderID = strings.TrimSpace(p.OrderID)
	if p.OrderID == "" {
		return p, ErrMissingID
	}
	return p, nil
}

// IsCancel reports whether the payload is a cancellation of an existing order.
func (p WebhookPayload) IsCancel() bool {
	for _, u := range p.Update {
		if strings.EqualFold(u, "cancelled") || strings.EqualFold(u, "canceled") {
			return true
		}
	}
	return false
}

// WebhookOrder maps a webhook payload to the canonical order.
func WebhookOrder(p WebhookPayload) Order {
	o := Order{
		Source:     "webhook",
		ExternalID: p.OrderID,
		Channel:    p.OrderSource,
		Restaurant: p.Restaurant,
		First:      p.FirstName,
		Last:       p.LastName,
		Total:      p.Total,
		Notes:      p.Notes,
		Kind:       "realtime",
	}
	o.Items = itemsFromNames(p.Items)
	o.Flags, o.NeedsReview = NoteFlags(p.Notes)
	return o
}

// RejectReason returns why an order cannot be fulfilled, or "" if it is fine.
func RejectReason(o Order) string {
	if len(o.Items) == 0 {
		return "empty_order"
	}
	if o.Total != nil && *o.Total <= 0 {
		return "zero_total"
	}
	return ""
}

// itemsFromNames collapses repeated names into quantities, keeping first-seen order.
func itemsFromNames(names []string) []Item {
	var items []Item
	idx := map[string]int{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if i, ok := idx[n]; ok {
			items[i].Qty++
			continue
		}
		idx[n] = len(items)
		items = append(items, Item{
			Key:      fmt.Sprintf("%d", len(items)+1),
			Name:     n,
			Category: Menu[n],
			Qty:      1,
		})
	}
	return items
}
