package httpapi

import (
	"context"
	"encoding/csv"
	"fmt"
	"postbox/internal/subscribers"
	"reflect"
	"strings"
	"testing"
)

func TestSubscriberExportStatusAndScope(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Export", FromEmail: "export@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Other readers", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := service.CreateList(ctx, subscribers.List{BrandID: other.ID, Name: "Foreign", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Writer.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Include people without memberships and an opt-out with a subscribed membership.
	for i, row := range []struct {
		email, status, member string
		list                  int64
		optedOut              bool
	}{
		{"active@example.org", "active", "subscribed", list.ID, false},
		{"bounced@example.org", "bounced", "subscribed", list.ID, false},
		{"complained@example.org", "complained", "unsubscribed", list.ID, false},
		{"blocked@example.org", "blocklisted", "subscribed", list.ID, false},
		{"unsubscribed@example.org", "active", "unsubscribed", list.ID, false},
		{"unconfirmed@example.org", "active", "unconfirmed", list.ID, false},
		{"brand-optout@example.org", "active", "subscribed", list.ID, true},
		{"other-list@example.org", "active", "unsubscribed", second.ID, false},
		{"no-list@example.org", "active", "", 0, true},
		{"no-membership@example.org", "active", "", 0, false},
	} {
		id := i + 1
		exec("INSERT INTO subscribers(id,brand_id,email,status,created_at,updated_at) VALUES(?,?,?,?,1,1)", id, brand.ID, row.email, row.status)
		if row.member != "" {
			exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(?,?,?,1,1)", row.list, id, row.member)
		}
		if row.optedOut {
			exec("UPDATE subscribers SET marketing_unsubscribed_at=2 WHERE id=?", id)
		}
	}
	exec("INSERT INTO subscribers(id,brand_id,email,status,created_at,updated_at) VALUES(11,?,'foreign@example.org','complained',1,1)", other.ID)
	exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(?,11,'unsubscribed',1,1)", foreign.ID)
	// Duplicate memberships must never produce duplicate CSV rows.
	exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(?,5,'unsubscribed',1,1)", second.ID)
	base := fmt.Sprintf("/api/v1/subscribers/export?brand_id=%d", brand.ID)
	for _, tc := range []struct {
		name, query string
		code        int
		emails      []string
	}{
		{"all", "", 200, []string{"active@example.org", "bounced@example.org", "complained@example.org", "blocked@example.org", "unsubscribed@example.org", "unconfirmed@example.org", "brand-optout@example.org", "other-list@example.org", "no-list@example.org", "no-membership@example.org"}},
		{"complained", "&status=complained", 200, []string{"complained@example.org"}},
		{"bounced", "&status=bounced", 200, []string{"bounced@example.org"}},
		{"blocklisted", "&status=blocklisted", 200, []string{"blocked@example.org"}},
		{"active delivery", "&status=active", 200, []string{"active@example.org", "unsubscribed@example.org", "unconfirmed@example.org", "brand-optout@example.org", "other-list@example.org", "no-list@example.org", "no-membership@example.org"}},
		{"subscribed", "&status=subscribed", 200, []string{"active@example.org"}},
		{"unconfirmed", "&status=unconfirmed", 200, []string{"unconfirmed@example.org"}},
		{"unsubscribed", "&status=unsubscribed", 200, []string{"complained@example.org", "unsubscribed@example.org", "brand-optout@example.org", "other-list@example.org", "no-list@example.org"}},
		{"list unsubscribed", fmt.Sprintf("&list_id=%d&status=unsubscribed", list.ID), 200, []string{"complained@example.org", "unsubscribed@example.org", "brand-optout@example.org"}},
		{"list subscribed", fmt.Sprintf("&list_id=%d&status=subscribed", list.ID), 200, []string{"active@example.org"}},
		{"list unconfirmed", fmt.Sprintf("&list_id=%d&status=unconfirmed", list.ID), 200, []string{"unconfirmed@example.org"}},
		{"list complained", fmt.Sprintf("&list_id=%d&status=complained", list.ID), 200, []string{"complained@example.org"}},
		{"empty", fmt.Sprintf("&list_id=%d&status=bounced", second.ID), 200, []string{}},
		{"foreign list", fmt.Sprintf("&list_id=%d", foreign.ID), 404, nil},
		{"missing list", "&list_id=99999", 404, nil},
		{"invalid status", "&status=invalid", 400, nil},
		{"invalid list", "&list_id=abc", 400, nil},
		{"zero list", "&list_id=0", 400, nil},
		{"negative list", "&list_id=-1", 400, nil},
		{"overflow list", "&list_id=99999999999999999999999", 400, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(mux, "GET", base+tc.query, "", &cookie, "")
			if w.Code != tc.code {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			if tc.code != 200 {
				if w.Header().Get("Content-Disposition") != "" || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
					t.Fatalf("error offered as CSV: %v", w.Header())
				}
				return
			}
			if w.Header().Get("Content-Type") != "text/csv" {
				t.Fatal(w.Header())
			}
			rows, err := csv.NewReader(w.Body).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, row := range rows[1:] {
				got = append(got, row[0])
				if (row[0] == "brand-optout@example.org" || row[0] == "no-list@example.org") && row[2] != "unsubscribed" {
					t.Fatalf("lost brand opt-out: %v", row)
				}
				if row[0] == "complained@example.org" && row[2] != "complained" {
					t.Fatalf("lost complaint status: %v", row)
				}
			}
			if !reflect.DeepEqual(got, tc.emails) {
				t.Fatalf("emails %v, want %v", got, tc.emails)
			}
		})
	}
}
