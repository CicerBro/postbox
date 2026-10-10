package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"testing"
)

func TestMembershipEditRenewsListOnlyButPreservesBrandOptOut(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	s := subscribers.New(db)
	b, err := s.CreateBrand(ctx, subscribers.Brand{Name: "brand", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.CreateList(ctx, subscribers.List{BrandID: b.ID, Name: "list", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: b.ID, Email: "member@example.org", ListIDs: []int64{l.ID}})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/subscribers/%d/memberships?brand_id=%d", v.ID, b.ID)
	body := fmt.Sprintf(`[{"list_id":%d,"status":"subscribed"}]`, l.ID)
	if err = s.Unsubscribe(ctx, b.ID, v.ID, l.ID); err != nil {
		t.Fatal(err)
	}
	w := request(mux, "PUT", path, body, &cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("renew list %d %s", w.Code, w.Body.String())
	}
	var state string
	if err = db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE subscriber_id=? AND list_id=?`, v.ID, l.ID).Scan(&state); err != nil || state != "subscribed" {
		t.Fatalf("state %s %v", state, err)
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	w = request(mux, "PUT", path, body, &cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("edit brand %d %s", w.Code, w.Body.String())
	}
	if err = db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE subscriber_id=? AND list_id=?`, v.ID, l.ID).Scan(&state); err != nil || state != "unsubscribed" {
		t.Fatalf("revived brand opt-out %s %v", state, err)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/subscribers/%d?brand_id=%d", v.ID, b.ID), "", &cookie, "")
	var rows []struct {
		MarketingUnsubscribedAt *int64 `json:"marketing_unsubscribed_at"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].MarketingUnsubscribedAt == nil {
		t.Fatalf("typed profile %d %s", w.Code, w.Body.String())
	}
	w = request(mux, "GET", path, "", &cookie, "")
	var memberships []struct {
		ListID int64  `json:"list_id"`
		Status string `json:"status"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &memberships) != nil || len(memberships) != 1 || memberships[0].ListID != l.ID || memberships[0].Status != "unsubscribed" {
		t.Fatalf("typed memberships %d %s", w.Code, w.Body.String())
	}
}
