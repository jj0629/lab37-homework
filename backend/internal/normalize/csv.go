package normalize

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// CSVHeader is the survey export's required header row.
var CSVHeader = []string{"first_name", "last_name", "items", "notes", "tomorrow", "meal"}

// CSVRow is one parsed survey response.
type CSVRow struct {
	Line       int      `json:"line"`
	First      string   `json:"first_name"`
	Last       string   `json:"last_name"`
	Items      []string `json:"items"`
	RawItems   string   `json:"raw_items"`
	Notes      string   `json:"notes"`
	Tomorrow   bool     `json:"tomorrow"`
	Meal       string   `json:"meal"`
	ExternalID string   `json:"external_id"` // rowhash-occurrence
}

// Rejection explains why a CSV row was not accepted.
type Rejection struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
	Raw    string `json:"raw,omitempty"`
}

// ErrBadHeader means the file is not a survey export at all.
var ErrBadHeader = errors.New("unexpected CSV header")

// ParseCSV reads a survey export. Rows are keyed by a hash of their
// normalised content plus the occurrence number of that content within the
// file, so re-uploading a cumulative export produces the same keys.
func ParseCSV(r io.Reader, meals map[string]string) ([]CSVRow, []Rejection, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrBadHeader, err)
	}
	for i := range header {
		header[i] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header[i], "\ufeff")))
	}
	if strings.Join(header, ",") != strings.Join(CSVHeader, ",") {
		return nil, nil, fmt.Errorf("%w: got %q, want %q", ErrBadHeader, strings.Join(header, ","), strings.Join(CSVHeader, ","))
	}

	var rows []CSVRow
	var rejected []Rejection
	seen := map[string]int{}
	for {
		line, _ := cr.FieldPos(0)
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			rejected = append(rejected, Rejection{Line: line, Reason: "malformed row: " + err.Error()})
			continue
		}
		if len(rec) != len(CSVHeader) {
			rejected = append(rejected, Rejection{Line: line, Reason: fmt.Sprintf("expected %d columns, got %d", len(CSVHeader), len(rec)), Raw: strings.Join(rec, ",")})
			continue
		}
		row := CSVRow{
			Line:     line,
			First:    strings.TrimSpace(rec[0]),
			Last:     strings.TrimSpace(rec[1]),
			RawItems: rec[2],
			Items:    SplitItems(rec[2]),
			Notes:    strings.TrimSpace(rec[3]),
			Meal:     strings.ToLower(strings.TrimSpace(rec[5])),
		}
		switch strings.ToLower(strings.TrimSpace(rec[4])) {
		case "true", "yes", "1":
			row.Tomorrow = true
		case "false", "no", "0", "":
		default:
			rejected = append(rejected, Rejection{Line: line, Reason: "tomorrow must be true/false", Raw: strings.Join(rec, ",")})
			continue
		}
		var reason string
		switch {
		case row.First == "" && row.Last == "":
			reason = "missing customer name"
		case len(row.Items) == 0:
			reason = "no items"
		case meals[row.Meal] == "":
			reason = fmt.Sprintf("unknown meal %q", row.Meal)
		}
		if reason != "" {
			rejected = append(rejected, Rejection{Line: line, Reason: reason, Raw: strings.Join(rec, ",")})
			continue
		}
		key := csvRowHash(row)
		seen[key]++
		row.ExternalID = fmt.Sprintf("%s-%d", key[:16], seen[key])
		rows = append(rows, row)
	}
	return rows, rejected, nil
}

func csvRowHash(r CSVRow) string {
	parts := []string{
		strings.ToLower(r.First), strings.ToLower(r.Last),
		strings.ToLower(strings.Join(r.Items, "\x1f")),
		r.Notes, fmt.Sprint(r.Tomorrow), r.Meal,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(sum[:])
}

// SplitItems splits a free-text item list. Items are separated by newlines or
// commas, but some menu names contain commas ("Bone-in wings (6, 12, 24)",
// "Two eggs any style with bacon or sausage, hash browns, and toast").
// Strategy: split each line on commas that are outside parentheses, then
// greedily re-join adjacent fragments whenever the joined text is a known
// menu item (longest match first). Unknown fragments are kept as-is.
func SplitItems(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		frags := splitOutsideParens(line)
		for i := 0; i < len(frags); {
			taken := 1
			for j := len(frags); j > i+1; j-- {
				if _, ok := Menu[strings.Join(frags[i:j], ", ")]; ok {
					taken = j - i
					break
				}
			}
			out = append(out, strings.Join(frags[i:i+taken], ", "))
			i += taken
		}
	}
	return out
}

func splitOutsideParens(line string) []string {
	var frags []string
	depth, start := 0, 0
	for i, r := range line {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				frags = appendTrimmed(frags, line[start:i])
				start = i + 1
			}
		}
	}
	return appendTrimmed(frags, line[start:])
}

func appendTrimmed(frags []string, s string) []string {
	if s = strings.TrimSpace(s); s != "" {
		frags = append(frags, s)
	}
	return frags
}

// CSVOrder maps a parsed row to a scheduled canonical order.
func CSVOrder(r CSVRow, now time.Time, loc *time.Location, meals map[string]string) Order {
	o := Order{
		Source:     "csv",
		ExternalID: r.ExternalID,
		Channel:    "survey",
		First:      r.First,
		Last:       r.Last,
		Notes:      r.Notes,
		Kind:       "scheduled",
		Meal:       r.Meal,
		Items:      itemsFromNames(r.Items),
	}
	o.Flags, o.NeedsReview = NoteFlags(r.Notes)
	slot, moved := SlotFor(now, loc, meals, r.Meal, r.Tomorrow)
	o.ScheduledFor = &slot
	if moved {
		o.Flags = append(o.Flags, "slot_passed_rescheduled")
		o.NeedsReview = true
	}
	for _, it := range o.Items {
		if _, known := Menu[it.Name]; !known {
			o.Flags = append(o.Flags, "unknown_item")
			break
		}
	}
	return o
}

// SlotFor computes when a scheduled order is due. "tomorrow" picks the meal
// on the next calendar day; otherwise today's meal. If today's slot has
// already passed, the order moves to the next occurrence of the same meal
// and the caller flags it.
func SlotFor(now time.Time, loc *time.Location, meals map[string]string, meal string, tomorrow bool) (time.Time, bool) {
	hm, _ := time.Parse("15:04", meals[meal])
	local := now.In(loc)
	day := 0
	if tomorrow {
		day = 1
	}
	slot := time.Date(local.Year(), local.Month(), local.Day()+day, hm.Hour(), hm.Minute(), 0, 0, loc)
	if !tomorrow && now.After(slot) {
		return time.Date(local.Year(), local.Month(), local.Day()+1, hm.Hour(), hm.Minute(), 0, 0, loc), true
	}
	return slot, false
}
