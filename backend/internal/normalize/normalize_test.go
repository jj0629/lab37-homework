package normalize

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var meals = map[string]string{"breakfast": "08:00", "lunch": "12:00", "dinner": "18:00"}

func TestSplitItems(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Espresso, Croissant", []string{"Espresso", "Croissant"}},
		{"Espresso\nCroissant\n", []string{"Espresso", "Croissant"}},
		{"Milkshakes (vanilla, chocolate, strawberry), Patty melt",
			[]string{"Milkshakes (vanilla, chocolate, strawberry)", "Patty melt"}},
		// Comma inside a menu name and outside parentheses.
		{"Croissant, Chicken lo mein, Two eggs any style with bacon or sausage, hash browns, and toast, Baguette",
			[]string{"Croissant", "Chicken lo mein", "Two eggs any style with bacon or sausage, hash browns, and toast", "Baguette"}},
		{"Two eggs any style with bacon or sausage, hash browns, and toast\nRye loaf",
			[]string{"Two eggs any style with bacon or sausage, hash browns, and toast", "Rye loaf"}},
		// Unknown fragments are kept rather than dropped.
		{"Mystery dish, Espresso", []string{"Mystery dish", "Espresso"}},
		{"  ,  ", nil},
	}
	for _, c := range cases {
		if got := SplitItems(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitItems(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestSampleCSVsAllItemsKnownAndKeysStable(t *testing.T) {
	var prev []CSVRow
	for _, n := range []string{"1", "2", "3", "4"} {
		f, err := os.Open("../../../sample-data/orders_" + n + ".csv")
		if err != nil {
			t.Skip("sample data not found:", err)
		}
		rows, rej, err := ParseCSV(f, meals)
		f.Close()
		if err != nil || len(rej) != 0 {
			t.Fatalf("file %s: err=%v rejected=%v", n, err, rej)
		}
		for _, r := range rows {
			for _, it := range r.Items {
				if _, ok := Menu[it]; !ok {
					t.Errorf("file %s line %d: unknown item %q", n, r.Line, it)
				}
			}
		}
		// Files are cumulative: every earlier row must keep the same key.
		for i := range prev {
			if prev[i].ExternalID != rows[i].ExternalID {
				t.Fatalf("file %s row %d key changed", n, i)
			}
		}
		prev = rows
	}
	if len(prev) != 267 {
		t.Fatalf("orders_4 rows = %d, want 267", len(prev))
	}
}

func TestCSVRejectsAndOccurrence(t *testing.T) {
	in := "first_name,last_name,items,notes,tomorrow,meal\n" +
		"A,B,Espresso,,true,lunch\n" +
		"A,B,Espresso,,true,lunch\n" + // identical row: second occurrence, distinct key
		"C,D,,,true,lunch\n" + // no items
		"E,F,Espresso,,maybe,lunch\n" + // bad bool
		"G,H,Espresso,,false,brunch\n" // unknown meal
	rows, rej, err := ParseCSV(strings.NewReader(in), meals)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(rej) != 3 {
		t.Fatalf("rows=%d rej=%d (%v)", len(rows), len(rej), rej)
	}
	if rows[0].ExternalID == rows[1].ExternalID || !strings.HasSuffix(rows[1].ExternalID, "-2") {
		t.Fatalf("occurrence keys not distinct: %s %s", rows[0].ExternalID, rows[1].ExternalID)
	}
	if _, _, err := ParseCSV(strings.NewReader("a,b\n1,2\n"), meals); err == nil {
		t.Fatal("expected header error")
	}
}

func TestSlotFor(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	at := func(h, m int) time.Time { return time.Date(2026, 10, 9, h, m, 0, 0, loc) }

	slot, moved := SlotFor(at(9, 0), loc, meals, "lunch", false)
	if moved || !slot.Equal(at(12, 0)) {
		t.Errorf("today lunch: %v moved=%v", slot, moved)
	}
	slot, moved = SlotFor(at(9, 0), loc, meals, "breakfast", false)
	if !moved || !slot.Equal(at(8, 0).AddDate(0, 0, 1)) {
		t.Errorf("passed breakfast should move to tomorrow: %v moved=%v", slot, moved)
	}
	slot, moved = SlotFor(at(23, 0), loc, meals, "breakfast", true)
	if moved || !slot.Equal(at(8, 0).AddDate(0, 0, 1)) {
		t.Errorf("tomorrow breakfast: %v moved=%v", slot, moved)
	}
	// Inside the lead window but before the slot is still today.
	slot, moved = SlotFor(at(11, 45), loc, meals, "lunch", false)
	if moved || !slot.Equal(at(12, 0)) {
		t.Errorf("lead window: %v moved=%v", slot, moved)
	}
}

func TestWebhook(t *testing.T) {
	p, err := ParseWebhook([]byte(`{"order_id":"x","order_source":"Overeats","total":10,"items":["Espresso","Espresso","Croissant"],"notes":"I know I added Espresso, but..."}`))
	if err != nil {
		t.Fatal(err)
	}
	o := WebhookOrder(p)
	if len(o.Items) != 2 || o.Items[0].Qty != 2 {
		t.Errorf("qty collapse failed: %+v", o.Items)
	}
	if !o.NeedsReview || o.Flags[0] != "item_change_requested" {
		t.Errorf("review flag missing: %+v", o.Flags)
	}
	empty, _ := ParseWebhook([]byte(`{"order_id":"y","total":0,"items":[]}`))
	if RejectReason(WebhookOrder(empty)) != "empty_order" {
		t.Error("empty order not rejected")
	}
	c, _ := ParseWebhook([]byte(`{"order_id":"z","update":["cancelled"]}`))
	if !c.IsCancel() {
		t.Error("cancel not detected")
	}
	if _, err := ParseWebhook([]byte(`{"total":1}`)); err != ErrMissingID {
		t.Errorf("want ErrMissingID, got %v", err)
	}
}

func TestPayloadHashIgnoresKeyOrder(t *testing.T) {
	a := PayloadHash([]byte(`{"a":1,"b":[1,2]}`))
	b := PayloadHash([]byte(`{ "b":[1,2], "a":1 }`))
	if a != b {
		t.Error("hash depends on key order")
	}
}

func TestExternalStatus(t *testing.T) {
	if LaterExternal("with_courier", "processing") != "with_courier" {
		t.Error("status moved backwards")
	}
	if LaterExternal("ordered", "delivered") != "delivered" {
		t.Error("status did not advance")
	}
	if got := AggregateExternal([]string{"delivered", "with_courier", "delivered"}); got != "with_courier" {
		t.Errorf("aggregate = %s", got)
	}
	if MapExternal("with_courier") != StatusOutForDelivery || MapExternal("delivered") != StatusCompleted || MapExternal("processing") != "" {
		t.Error("status mapping wrong")
	}
	if !IsForward(StatusQueued, StatusDispatched) || IsForward(StatusCompleted, StatusCancelled) || IsForward(StatusDispatched, StatusQueued) {
		t.Error("IsForward wrong")
	}
}

func TestGroupByOrder(t *testing.T) {
	d := GroupByOrder(map[string]APIItem{
		"h1": {Order: 2, Name: "a"}, "h2": {Order: 1, Name: "b"}, "h3": {Order: 2, Name: "c"},
	})
	if len(d) != 2 || d[0].Order != 1 || len(d[1].Items) != 2 {
		t.Fatalf("grouping wrong: %+v", d)
	}
}
