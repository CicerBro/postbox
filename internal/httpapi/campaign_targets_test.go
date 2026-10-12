package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"postbox/internal/send"
	"postbox/internal/subscribers"
)

func TestCampaignTargetReplacementValidatesBatchAtomically(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Campaign", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Included"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := service.CreateList(ctx, subscribers.List{BrandID: other.ID, Name: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,'target-test','draft','Subject','Body',1,1)`, brand.ID)
	if err != nil {
		t.Fatal(err)
	}
	campaign, _ := row.LastInsertId()
	path := fmt.Sprintf("/api/v1/campaigns/%d/targets?brand_id=%d", campaign, brand.ID)
	valid := []send.AudienceTarget{{Type: "list", ID: list.ID, Mode: "include"}}
	body, _ := json.Marshal(valid)
	if response := request(mux, "PUT", path, string(body), &cookie, csrf); response.Code != 200 {
		t.Fatalf("valid target rejected: %d %s", response.Code, response.Body)
	}
	for _, target := range []send.AudienceTarget{
		{Type: "list", ID: foreign.ID, Mode: "exclude"},
		{Type: "list", ID: 99999, Mode: "include"},
		{Type: "segment", ID: list.ID, Mode: "include"},
		{Type: "list", ID: list.ID, Mode: "invalid"},
	} {
		body, _ := json.Marshal(append(valid[:1:1], target))
		if response := request(mux, "PUT", path, string(body), &cookie, csrf); response.Code == 200 {
			t.Fatalf("invalid target accepted: %+v", target)
		}
		var id int64
		var count int
		if err := db.Reader.QueryRow(`SELECT count(*),min(target_id) FROM campaign_targets WHERE campaign_id=?`, campaign).Scan(&count, &id); err != nil || count != 1 || id != list.ID {
			t.Fatalf("failed batch changed targets: count=%d id=%d error=%v", count, id, err)
		}
	}
	if _, err := db.Writer.Exec(`UPDATE campaigns SET status='sent' WHERE id=?`, campaign); err != nil {
		t.Fatal(err)
	}
	if response := request(mux, "PUT", path, `[]`, &cookie, csrf); response.Code == 200 {
		t.Fatal("non-draft targets replaced")
	}
}
