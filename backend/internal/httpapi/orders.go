package httpapi

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/jj0629/lab37-homework/backend/internal/orders"
	"github.com/jj0629/lab37-homework/backend/internal/store"
)

// OrderSummary is one row in the orders list.
type OrderSummary struct {
	ID             int64      `json:"id"`
	Source         string     `json:"source"`
	ExternalID     string     `json:"external_id"`
	Channel        string     `json:"channel"`
	Restaurant     string     `json:"restaurant"`
	CustomerFirst  string     `json:"customer_first"`
	CustomerLast   string     `json:"customer_last"`
	ItemCount      int        `json:"item_count"`
	Total          *float64   `json:"total"`
	Status         string     `json:"status"`
	ExternalStatus string     `json:"external_status"`
	Kind           string     `json:"kind"`
	Meal           string     `json:"meal"`
	ScheduledFor   *time.Time `json:"scheduled_for"`
	Flags          []string   `json:"flags"`
	NeedsReview    bool       `json:"needs_review"`
	Notes          string     `json:"notes"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DispatchedAt   *time.Time `json:"dispatched_at"`
}

const summaryCols = `o.id, o.source, o.external_id, o.channel, o.restaurant, o.customer_first, o.customer_last,
	(SELECT coalesce(sum(qty),0) FROM order_items i WHERE i.order_id=o.id AND i.external_status <> 'cancelled'),
	o.total, o.status, o.external_status, o.kind, o.meal, o.scheduled_for, o.flags, o.needs_review, o.notes,
	o.created_at, o.updated_at, o.dispatched_at`

func scanSummary(sc interface{ Scan(...any) error }) (OrderSummary, error) {
	var o OrderSummary
	var total sql.NullFloat64
	var sched, disp sql.NullTime
	var flags pq.StringArray
	err := sc.Scan(&o.ID, &o.Source, &o.ExternalID, &o.Channel, &o.Restaurant, &o.CustomerFirst, &o.CustomerLast,
		&o.ItemCount, &total, &o.Status, &o.ExternalStatus, &o.Kind, &o.Meal, &sched, &flags, &o.NeedsReview, &o.Notes,
		&o.CreatedAt, &o.UpdatedAt, &disp)
	if total.Valid {
		o.Total = &total.Float64
	}
	if sched.Valid {
		o.ScheduledFor = &sched.Time
	}
	if disp.Valid {
		o.DispatchedAt = &disp.Time
	}
	o.Flags = []string(flags)
	if o.Flags == nil {
		o.Flags = []string{}
	}
	return o, err
}

// filters turns query parameters into a WHERE clause shared by list and export.
func filters(r *http.Request) (string, []any) {
	q := r.URL.Query()
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if v := q.Get("status"); v != "" {
		add("o.status = ANY($%d)", pq.Array(strings.Split(v, ",")))
	}
	if v := q.Get("source"); v != "" {
		add("o.source = ANY($%d)", pq.Array(strings.Split(v, ",")))
	}
	if v := q.Get("kind"); v != "" {
		add("o.kind = ANY($%d)", pq.Array(strings.Split(v, ",")))
	}
	if v := q.Get("needs_review"); v == "true" || v == "false" {
		add("o.needs_review = $%d", v == "true")
	}
	// flags: comma-separated, OR-ed. "needs_review" refers to the boolean
	// column; anything else must be present in the o.flags array.
	if v := q.Get("flags"); v != "" {
		var flags []string
		review := false
		for _, f := range strings.Split(v, ",") {
			if f = strings.TrimSpace(f); f == "needs_review" {
				review = true
			} else if f != "" {
				flags = append(flags, f)
			}
		}
		var or []string
		if review {
			or = append(or, "o.needs_review")
		}
		if len(flags) > 0 {
			args = append(args, pq.Array(flags))
			or = append(or, fmt.Sprintf("o.flags && $%d::text[]", len(args)))
		}
		if len(or) > 0 {
			where = append(where, "("+strings.Join(or, " OR ")+")")
		}
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		args = append(args, "%"+v+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(`(o.customer_first || ' ' || o.customer_last ILIKE $%[1]d OR o.external_id ILIKE $%[1]d
			OR o.restaurant ILIKE $%[1]d OR o.channel ILIKE $%[1]d OR o.id::text = $%[2]d)`, n, n+1))
		args = append(args, v)
	}
	if len(where) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

var sortable = map[string]string{
	"id": "o.id", "created_at": "o.created_at", "updated_at": "o.updated_at", "status": "o.status",
	"total": "o.total", "scheduled_for": "o.scheduled_for", "source": "o.source", "customer": "o.customer_last",
}

func orderBy(r *http.Request) string {
	col, ok := sortable[r.URL.Query().Get("sort")]
	if !ok {
		col = "o.id"
	}
	dir := "DESC"
	if r.URL.Query().Get("dir") == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf(" ORDER BY %s %s NULLS LAST, o.id %s", col, dir, dir)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	where, args := filters(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int
	if err := s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM orders o`+where, args...).Scan(&total); err != nil {
		internal(w, r, err)
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT `+summaryCols+` FROM orders o`+where+orderBy(r)+
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		internal(w, r, err)
		return
	}
	defer rows.Close()
	items := []OrderSummary{}
	for rows.Next() {
		o, err := scanSummary(rows)
		if err != nil {
			internal(w, r, err)
			return
		}
		items = append(items, o)
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

// exportCSV streams the filtered list as CSV, one row per order.
func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	where, args := filters(r)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT `+summaryCols+`,
		(SELECT string_agg(CASE WHEN qty > 1 THEN qty || ' x ' || name ELSE name END, '; ' ORDER BY id) FROM order_items i WHERE i.order_id=o.id)
		FROM orders o`+where+orderBy(r), args...)
	if err != nil {
		internal(w, r, err)
		return
	}
	defer rows.Close()
	name := "orders-" + time.Now().UTC().Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "source", "external_id", "channel", "customer", "restaurant", "items", "total",
		"status", "kind", "scheduled_for", "flags", "needs_review", "created_at", "updated_at"})
	fmtT := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	n := 0
	for rows.Next() {
		var o OrderSummary
		var total sql.NullFloat64
		var sched, disp sql.NullTime
		var flags pq.StringArray
		var items sql.NullString
		if err := rows.Scan(&o.ID, &o.Source, &o.ExternalID, &o.Channel, &o.Restaurant, &o.CustomerFirst, &o.CustomerLast,
			&o.ItemCount, &total, &o.Status, &o.ExternalStatus, &o.Kind, &o.Meal, &sched, &flags, &o.NeedsReview, &o.Notes,
			&o.CreatedAt, &o.UpdatedAt, &disp, &items); err != nil {
			return // headers already sent; truncated file is the best we can do
		}
		tot := ""
		if total.Valid {
			tot = strconv.FormatFloat(total.Float64, 'f', 2, 64)
		}
		var sp *time.Time
		if sched.Valid {
			sp = &sched.Time
		}
		_ = cw.Write([]string{strconv.FormatInt(o.ID, 10), o.Source, o.ExternalID, o.Channel,
			strings.TrimSpace(o.CustomerFirst + " " + o.CustomerLast), o.Restaurant, items.String, tot,
			o.Status, o.Kind, fmtT(sp), strings.Join(flags, ";"), strconv.FormatBool(o.NeedsReview),
			fmtT(&o.CreatedAt), fmtT(&o.UpdatedAt)})
		if n++; n%500 == 0 {
			cw.Flush()
		}
	}
	cw.Flush()
}

// Item is an order line in the detail view.
type Item struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Price          *float64 `json:"price"`
	Qty            int      `json:"qty"`
	ExternalStatus string   `json:"external_status"`
}

// Event is one entry in an order's history.
type Event struct {
	ID         int64           `json:"id"`
	Type       string          `json:"type"`
	FromStatus string          `json:"from_status"`
	ToStatus   string          `json:"to_status"`
	Message    string          `json:"message"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (s *Server) loadEvents(r *http.Request, id int64) ([]Event, error) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, type, from_status, to_status, message, payload, created_at
		FROM order_events WHERE order_id=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var p []byte
		if err := rows.Scan(&e.ID, &e.Type, &e.FromStatus, &e.ToStatus, &e.Message, &p, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(p) > 0 {
			e.Payload = p
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	o, err := scanSummary(s.DB.QueryRowContext(r.Context(), `SELECT `+summaryCols+` FROM orders o WHERE o.id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "order not found")
		return
	}
	if err != nil {
		internal(w, r, err)
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT item_key, name, category, price, qty, external_status
		FROM order_items WHERE order_id=$1 ORDER BY id`, id)
	if err != nil {
		internal(w, r, err)
		return
	}
	items := []Item{}
	for rows.Next() {
		var it Item
		var price sql.NullFloat64
		if err := rows.Scan(&it.Key, &it.Name, &it.Category, &price, &it.Qty, &it.ExternalStatus); err != nil {
			rows.Close()
			internal(w, r, err)
			return
		}
		if price.Valid {
			it.Price = &price.Float64
		}
		items = append(items, it)
	}
	rows.Close()

	events, err := s.loadEvents(r, id)
	if err != nil {
		internal(w, r, err)
		return
	}

	// Robot payload: what was actually sent if dispatched, else a live preview.
	var payload json.RawMessage
	var outStatus string
	var outAttempts int
	var outErr string
	err = s.DB.QueryRowContext(r.Context(), `SELECT payload, status, attempts, last_error FROM dispatch_outbox
		WHERE order_id=$1 AND kind='dispatch'`, id).Scan(&payload, &outStatus, &outAttempts, &outErr)
	if errors.Is(err, sql.ErrNoRows) {
		p, perr := orders.BuildRobotPayload(r.Context(), s.DB, id)
		if perr != nil {
			internal(w, r, perr)
			return
		}
		payload, _ = json.Marshal(p)
		outStatus = "preview"
	} else if err != nil {
		internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"order": o, "items": items, "events": events,
		"robot": map[string]any{"status": outStatus, "attempts": outAttempts, "last_error": outErr, "payload": payload},
	})
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	events, err := s.loadEvents(r, id)
	if err != nil {
		internal(w, r, err)
		return
	}
	writeJSON(w, 200, events)
}

// getRelated returns quarantined conflicts and dropped duplicates for an order.
func (s *Server) getRelated(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var source, ext string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT source, external_id FROM orders WHERE id=$1`, id).Scan(&source, &ext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, 404, "order not found")
			return
		}
		internal(w, r, err)
		return
	}
	conflicts, err := s.queryDeadLetter(r, ` WHERE source=$1 AND external_id=$2 AND reason='id_conflict'`, source, ext)
	if err != nil {
		internal(w, r, err)
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, received_at, upload_id, payload FROM inbox
		WHERE source=$1 AND external_id=$2 AND is_duplicate ORDER BY id`, source, ext)
	if err != nil {
		internal(w, r, err)
		return
	}
	defer rows.Close()
	type dup struct {
		InboxID    int64           `json:"inbox_id"`
		ReceivedAt time.Time       `json:"received_at"`
		UploadID   *int64          `json:"upload_id"`
		Payload    json.RawMessage `json:"payload"`
	}
	dups := []dup{}
	for rows.Next() {
		var d dup
		var up sql.NullInt64
		if err := rows.Scan(&d.InboxID, &d.ReceivedAt, &up, &d.Payload); err != nil {
			internal(w, r, err)
			return
		}
		if up.Valid {
			d.UploadID = &up.Int64
		}
		dups = append(dups, d)
	}
	writeJSON(w, 200, map[string]any{"conflicts": conflicts, "duplicates": dups, "duplicate_count": len(dups)})
}

// dispatchNow releases an order to the robot immediately ("dispatch now").
func (s *Server) dispatchNow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var moved bool
	var status string
	err = store.InTx(r.Context(), s.DB, func(tx *sql.Tx) error {
		l, err := orders.LockByID(r.Context(), tx, id)
		if err != nil {
			return err
		}
		status = l.Status
		if l.Status != "received" && l.Status != "scheduled" {
			return nil
		}
		moved, err = orders.Transition(r.Context(), tx, id, l.Status, "queued", "manual_dispatch", "dispatch requested by operator", nil)
		return err
	})
	if errors.Is(err, orders.ErrNotFound) {
		writeErr(w, 404, "order not found")
		return
	}
	if err != nil {
		internal(w, r, err)
		return
	}
	if !moved {
		writeErr(w, 409, "order is "+status+"; only received or scheduled orders can be dispatched early")
		return
	}
	writeJSON(w, 202, map[string]any{"id": id, "status": "queued"})
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = decodeBody(r, &body)
	if body.Reason == "" {
		body.Reason = "cancelled by operator"
	}
	var res orders.CancelResult
	err = store.InTx(r.Context(), s.DB, func(tx *sql.Tx) error {
		l, err := orders.LockByID(r.Context(), tx, id)
		if err != nil {
			return err
		}
		res, err = orders.Cancel(r.Context(), tx, l, body.Reason, nil)
		return err
	})
	if errors.Is(err, orders.ErrNotFound) {
		writeErr(w, 404, "order not found")
		return
	}
	if err != nil {
		internal(w, r, err)
		return
	}
	code := 200
	if res == orders.CancelIgnored {
		code = 409
	}
	writeJSON(w, code, map[string]any{"id": id, "result": res})
}
