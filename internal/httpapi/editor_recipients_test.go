package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/send"
	"postbox/internal/subscribers"
	"testing"
)

func TestEditorRecipientsRespectUnsavedAudience(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Preview", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Included"})
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Excluded"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 7 {
		lists := []int64{list.ID}
		if i == 2 {
			lists = append(lists, excluded.ID)
		}
		if i == 6 {
			lists = nil
		}
		sub, err := service.Upsert(ctx, subscribers.UpsertInput{BrandID: brand.ID, Email: fmt.Sprintf("reader%d@example.org", i), Name: "Reader", ListIDs: lists})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sub.ID)
	}
	if _, err := db.Writer.Exec(`UPDATE subscribers SET status='bounced' WHERE id=?`, ids[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE list_subscribers SET status='unsubscribed' WHERE subscriber_id=?`, ids[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(?,'email','reader5@example.org','manual',0)`, brand.ID); err != nil {
		t.Fatal(err)
	}
	res, err := db.Writer.Exec(`INSERT INTO segments(brand_id,name,rules,created_at) VALUES(?,'Included segment',?,0)`, brand.ID, fmt.Sprintf(`{"field":"list","op":"in","value":%d}`, list.ID))
	if err != nil {
		t.Fatal(err)
	}
	segment, _ := res.LastInsertId()
	path := fmt.Sprintf("/api/v1/email-preview/recipients?brand_id=%d", brand.ID)
	for _, kind := range []string{"list", "segment"} {
		targetID := list.ID
		if kind == "segment" {
			targetID = segment
		}
		targets := []send.AudienceTarget{{Type: kind, ID: targetID, Mode: "include"}, {Type: "list", ID: list.ID, Mode: "include"}, {Type: "list", ID: excluded.ID, Mode: "exclude"}}
		for _, page := range []struct {
			after int64
			want  []int64
		}{{0, ids[:2]}, {ids[0], ids[1:2]}, {ids[1], nil}, {0, ids[:2]}} {
			payload, _ := json.Marshal(map[string]any{"targets": targets, "after": page.after})
			w := request(mux, "POST", path, string(payload), &cookie, csrf)
			var out []send.PreviewRecipient
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != len(page.want) {
				t.Fatalf("%s page: %d %s", kind, w.Code, w.Body)
			}
			for i, id := range page.want {
				if out[i].ID != id {
					t.Fatalf("wrong subscriber: %+v", out)
				}
			}
		}
	}
	other, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := service.CreateList(ctx, subscribers.List{BrandID: other.ID, Name: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"targets": []send.AudienceTarget{{Type: "list", ID: foreign.ID, Mode: "include"}}})
	w := request(mux, "POST", path, string(payload), &cookie, csrf)
	if w.Code == 200 {
		t.Fatalf("foreign audience accepted: %s", w.Body)
	}
	for _, table := range []string{"campaign_targets", "campaigns", "campaign_recipients", "jobs"} {
		var count int
		if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("preview wrote %s: %d %v", table, count, err)
		}
	}
}
