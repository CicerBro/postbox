package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"testing"
)

func TestSubscriberSearchScopeAndListCounters(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	s := subscribers.New(db)
	ctx := context.Background()
	b, err := s.CreateBrand(ctx, subscribers.Brand{Name: "one", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.CreateBrand(ctx, subscribers.Brand{Name: "other", FromEmail: "other@example.org"})
	list, _ := s.CreateList(ctx, subscribers.List{BrandID: b.ID, Name: "members", OptIn: "single"})
	foreign, _ := s.CreateList(ctx, subscribers.List{BrandID: other.ID, Name: "foreign", OptIn: "single"})
	one, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: b.ID, Email: "match@example.org", ListIDs: []int64{list.ID}, Fields: map[string]any{"city": "London"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upsert(ctx, subscribers.UpsertInput{BrandID: other.ID, Email: "match@example.org"}); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/subscribers?brand_id=%d", b.ID)
	for _, tc := range []struct {
		query string
		code  int
		count int
	}{
		{fmt.Sprintf("&q=MATCH&list_id=%d&membership_status=subscribed&status=active", list.ID), 200, 1},
		{fmt.Sprintf("&q=match&after=%d", one.ID), 200, 0},
		{"&q=match%25", 200, 0},
		{fmt.Sprintf("&list_id=%d", foreign.ID), 404, 0},
		{"&list_id=abc", 400, 0}, {"&status=unknown", 400, 0}, {"&membership_status=subscribed", 400, 0},
	} {
		w := request(mux, "GET", base+tc.query, "", &cookie, "")
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
		if tc.code == 200 {
			var out []subscribers.Subscriber
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != tc.count {
				t.Fatalf("%s: %s %v", tc.query, w.Body.String(), err)
			}
			if len(out) > 0 && (out[0].BrandID != b.ID || out[0].Fields["city"] != "London") {
				t.Fatal(out)
			}
		}
	}
	if _, err = db.Writer.Exec("UPDATE list_counters SET active=7,subscribed=12,unsubscribed=5,bounced=2,complained=1 WHERE list_id=?", list.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/lists", fmt.Sprintf("/api/v1/lists/%d", list.ID)} {
		w := request(mux, "GET", fmt.Sprintf("%s?brand_id=%d", path, b.ID), "", &cookie, "")
		var out []map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != 1 || out[0]["active"] != float64(7) || out[0]["complained"] != float64(1) {
			t.Fatalf("counters: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err = db.Writer.Exec("DELETE FROM list_counters WHERE list_id=?", list.ID); err != nil {
		t.Fatal(err)
	}
	w := request(mux, "GET", fmt.Sprintf("/api/v1/lists?brand_id=%d", b.ID), "", &cookie, "")
	var out []map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != 1 || out[0]["active"] != float64(0) {
		t.Fatal(w.Body.String())
	}
}
