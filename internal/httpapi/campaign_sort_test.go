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
	// Drafts come first, then scheduled campaigns by send date, even when
	// the next campaign to send has the oldest creation date and lowest ID.
	for _, row := range []struct {
		subject, status string
		created         int
		scheduled       any
	}{
		{"Epsilon scheduled", "scheduled", 0, 50},
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
	} {
		t.Helper()
		w := request(mux, "GET", fmt.Sprintf("/api/v1/campaigns?brand_id=%d&%s", brand.ID, params), "", &cookie, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var rows []struct {
			ID      int
			Subject string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	first := read("offset=0&limit=2")
	second := read("offset=2&limit=2")
	if len(first) != 2 || len(second) != 2 || first[0].Subject != "Delta draft" || first[1].Subject != "Zulu draft" || second[0].Subject != "Epsilon scheduled" || second[1].Subject != "Beta scheduled" {
		t.Fatalf("priority pages: %v %v", first, second)
	}
	last := read("offset=4&limit=2")
	if len(last) != 1 || last[0].Subject != "Alpha sent" {
		t.Fatalf("sent campaigns follow scheduled campaigns: %v", last)
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
	if len(fallback) != 5 || fallback[0].Subject != "Delta draft" {
		t.Fatalf("fallback: %v", fallback)
	}
	legacy := read("after=1&limit=2")
	if len(legacy) != 2 || legacy[0].ID != 2 || legacy[1].ID != 3 {
		t.Fatalf("legacy cursor: %v", legacy)
	}
	// Joining live counters must preserve the same page and sort contracts.
	summary := read("offset=1&limit=2&sort=subject&direction=asc&summary=true")
	if len(summary) != 2 || summary[0].Subject != "Beta scheduled" || summary[1].Subject != "Delta draft" {
		t.Fatalf("summary sorted page: %v", summary)
	}
	summary = read("offset=0&limit=2&summary=true")
	if len(summary) != 2 || summary[0].Subject != "Delta draft" || summary[1].Subject != "Zulu draft" {
		t.Fatalf("summary priority page: %v", summary)
	}
	summary = read("offset=2&limit=3&sort=priority&direction=desc&summary=true")
	if len(summary) != 3 || summary[0].Subject != "Epsilon scheduled" || summary[1].Subject != "Beta scheduled" || summary[2].Subject != "Alpha sent" {
		t.Fatalf("summary scheduled and sent order: %v", summary)
	}
}

func TestCampaignSummaryIncludesFinishedDeliveryMetrics(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Brand", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, campaign := range []struct {
		id, status string
		finished   any
	}{
		{"scheduled", "scheduled", nil},
		{"sent", "sent", int64(1700000000000)},
	} {
		_, err := db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at,scheduled_at,finished_at) VALUES(?,?,?,?,?,1,1,?,?)", brand.ID, campaign.id, campaign.status, campaign.id, "<p>Hello</p>", int64(1700000010000), campaign.finished)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Writer.Exec("INSERT INTO campaign_stats(campaign_id,recipients,sent,opens_unique,clicks_unique) SELECT id,1200,1180,400,75 FROM campaigns WHERE uuid='sent'"); err != nil {
		t.Fatal(err)
	}
	w := request(mux, "GET", fmt.Sprintf("/api/v1/campaigns?brand_id=%d&offset=0&summary=true", brand.ID), "", &cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var rows []struct {
		Status     string `json:"status"`
		FinishedAt *int64 `json:"finished_at"`
		Stats      *struct {
			Recipients   int64 `json:"recipients"`
			Sent         int64 `json:"sent"`
			OpensUnique  int64 `json:"opens_unique"`
			ClicksUnique int64 `json:"clicks_unique"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Status != "scheduled" || rows[0].Stats != nil || rows[0].FinishedAt != nil || rows[1].Status != "sent" || rows[1].FinishedAt == nil || *rows[1].FinishedAt != 1700000000000 || rows[1].Stats == nil || rows[1].Stats.Recipients != 1200 || rows[1].Stats.Sent != 1180 || rows[1].Stats.OpensUnique != 400 || rows[1].Stats.ClicksUnique != 75 {
		t.Fatalf("unexpected campaign summaries: %+v", rows)
	}
}
