package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"testing"
)

func TestCampaignTableSortingAndPagination(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Brand", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	// Older drafts still precede newer scheduled and sent campaigns.
	for _, row := range []struct {
		subject, status string
		created         int
		scheduled       any
	}{
		{"Zulu draft", "draft", 1, nil},
		{"Alpha sent", "sent", 4, nil},
		{"Beta scheduled", "scheduled", 3, 100},
		{"Delta draft", "draft", 2, nil},
	} {
		_, err := db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at,scheduled_at) VALUES(?,?,?,?,?,?,?,?)", brand.ID, row.subject, row.status, row.subject, "<p>Hello</p>", row.created, row.created, row.scheduled)
		if err != nil {
			t.Fatal(err)
		}
	}
	read := func(params string) []struct {
		ID      int
		Subject string
	} { t.Helper(); w := request(mux, "GET", fmt.Sprintf("/api/v1/campaigns?brand_id=%d&%s", brand.ID, params), "", &cookie, ""); if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}; var rows []struct {
		ID      int
		Subject string
	}; if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}; return rows }
	first := read("offset=0&limit=2")
	second := read("offset=2&limit=2")
	if len(first) != 2 || len(second) != 2 || first[0].Subject != "Delta draft" || first[1].Subject != "Zulu draft" || second[0].Subject != "Alpha sent" {
		t.Fatalf("priority pages: %v %v", first, second)
	}
	sorted := read("offset=1&limit=2&sort=subject&direction=asc")
	if len(sorted) != 2 || sorted[0].Subject != "Beta scheduled" || sorted[1].Subject != "Delta draft" {
		t.Fatalf("sorted page: %v", sorted)
	}
	filtered := read("offset=0&status=draft&search=zulu")
	if len(filtered) != 1 || filtered[0].Subject != "Zulu draft" {
		t.Fatalf("filtered rows: %v", filtered)
	}
	// Unknown sort names never enter SQL; legacy cursors remain ordered by ID.
	fallback := read("offset=0&sort=invalid")
	if len(fallback) != 4 || fallback[0].Subject != "Delta draft" {
		t.Fatalf("fallback: %v", fallback)
	}
	legacy := read("after=1&limit=2")
	if len(legacy) != 2 || legacy[0].ID != 2 || legacy[1].ID != 3 {
		t.Fatalf("legacy cursor: %v", legacy)
	}
}
