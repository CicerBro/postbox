package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"testing"
)

func TestBulkSubscriberAPIIsAtomicAndBrandScoped(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	s := subscribers.New(db)
	brand, err := s.CreateBrand(ctx, subscribers.Brand{Name: "Scoped", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/subscribers/bulk?brand_id=%d", brand.ID)
	failed := request(mux, "POST", path, `[{"email":"first@example.org"},{"email":"invalid@example.org","status":"invalid"}]`, &cookie, csrf)
	if failed.Code < 400 {
		t.Fatalf("invalid bulk=%d %s", failed.Code, failed.Body.String())
	}
	var count int
	if err := db.Reader.QueryRow("SELECT count(*) FROM subscribers").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed request retained %d subscribers: %v", count, err)
	}
	success := request(mux, "POST", path, fmt.Sprintf(`[{"brand_id":%d,"email":"ONE@example.org"},{"email":"two@example.org"}]`, other.ID), &cookie, csrf)
	var out []subscribers.Subscriber
	if success.Code != 200 || json.Unmarshal(success.Body.Bytes(), &out) != nil || len(out) != 2 {
		t.Fatalf("bulk=%d %s", success.Code, success.Body.String())
	}
	if out[0].BrandID != brand.ID || out[1].BrandID != brand.ID || out[0].Email != "one@example.org" || out[1].Email != "two@example.org" {
		t.Fatalf("scope or input order changed: %+v", out)
	}
}
