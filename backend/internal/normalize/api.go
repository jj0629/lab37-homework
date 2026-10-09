package normalize

import (
	"sort"
	"strconv"
)

// APIResponse is one response from the upstream order API (one line of api_responses.jsonl).
type APIResponse struct {
	Response int                `json:"response"`
	Data     map[string]APIItem `json:"data,omitempty"`
	Error    string             `json:"error,omitempty"`
	AsOf     string             `json:"as_of,omitempty"`    // cursor to send next time (if the API provides one)
	HasMore  bool               `json:"has_more,omitempty"` // more deltas are ready; poll again immediately
}

// APIItem is an item-level delta keyed by the upstream item hash.
type APIItem struct {
	Order    int     `json:"order"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Price    float64 `json:"price"`
	Status   string  `json:"status"`
}

// APIDelta groups one response's item deltas for a single upstream order.
type APIDelta struct {
	Order int                `json:"order"`
	Items map[string]APIItem `json:"items"`
}

// ExternalID is the order's id within the "api" source.
func (d APIDelta) ExternalID() string { return strconv.Itoa(d.Order) }

// GroupByOrder splits a response into per-order deltas, sorted by order number.
func GroupByOrder(data map[string]APIItem) []APIDelta {
	by := map[int]map[string]APIItem{}
	for hash, it := range data {
		if by[it.Order] == nil {
			by[it.Order] = map[string]APIItem{}
		}
		by[it.Order][hash] = it
	}
	out := make([]APIDelta, 0, len(by))
	for n, items := range by {
		out = append(out, APIDelta{Order: n, Items: items})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// externalRank orders upstream item statuses; they only move forward.
var externalRank = map[string]int{
	"ordered":      0,
	"processing":   1,
	"with_courier": 2,
	"delivered":    3,
}

// ExternalRank returns the rank of an upstream status (-1 if unknown).
func ExternalRank(s string) int {
	if r, ok := externalRank[s]; ok {
		return r
	}
	return -1
}

// LaterExternal returns whichever upstream status is further along. Used so
// a delayed or replayed delta can never move an item backwards.
func LaterExternal(cur, next string) string {
	if cur == "cancelled" {
		return cur
	}
	if next == "cancelled" || ExternalRank(next) > ExternalRank(cur) {
		return next
	}
	return cur
}

// AggregateExternal derives an order-level upstream status from its items:
// the least-advanced item wins (an order is delivered when every item is).
// Cancelled items are ignored unless every item is cancelled.
func AggregateExternal(statuses []string) string {
	best, bestRank, cancelled := "", 1<<30, 0
	for _, s := range statuses {
		if s == "cancelled" {
			cancelled++
			continue
		}
		if r := ExternalRank(s); r >= 0 && r < bestRank {
			best, bestRank = s, r
		}
	}
	if best == "" && cancelled > 0 {
		return "cancelled"
	}
	return best
}

// MapExternal maps an upstream order status onto our lifecycle. "ordered" and
// "processing" mean the kitchen has it; we dispatch to the robot on first
// sight, so they add no further transition.
func MapExternal(ext string) string {
	switch ext {
	case "with_courier":
		return StatusOutForDelivery
	case "delivered":
		return StatusCompleted
	case "cancelled":
		return StatusCancelled
	}
	return ""
}
