package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestCampaignReportRecentRecipientsAndDelivery(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	service := subscribers.New(db)
	brand, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Report", FromName: "Current", FromEmail: "current@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Writer.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for id := 1; id <= 2; id++ {
		exec("INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,?,?,'sent','Report','<p>Saved</p>',1,1)", id, brand.ID, fmt.Sprintf("report-%d", id))
	}
	exec("INSERT INTO campaign_preparations(campaign_id,subject,html,text,from_name,from_email,tracking_url,prepared_at) VALUES(1,'Report','<p>Saved</p>','','Historical','historical@example.org','https://example.org',1)")
	for id := 1; id <= 12; id++ {
		exec("INSERT INTO subscribers(id,brand_id,email,name,created_at,updated_at) VALUES(?,?,?,'Current name',1,1)", id, brand.ID, fmt.Sprintf("current%d@example.org", id))
		exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,snapshot_email,snapshot_name,sent_at,first_opened_at,unsubscribed_at,bounced_at,complained_at) VALUES(1,?,'sent',?,'Snapshot name',?,?,?,?,?)`, id, fmt.Sprintf("sent%d@example.org", id), 1000+id, 2000+id, 3000+id, 4000+id, 5000+id)
		// Repeated raw opens and another campaign must not duplicate or reorder report samples.
		for repeat := 0; repeat < 2; repeat++ {
			exec("INSERT INTO opens(campaign_id,subscriber_id,at) VALUES(1,?,99999)", id)
		}
	}
	exec("INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,first_opened_at,unsubscribed_at,bounced_at,complained_at) VALUES(2,1,'sent',99999,99999,99999,99999)")
	exec("INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(99,?,'foreign@example.org',1,1)", other.ID)
	// Even malformed legacy recipient rows cannot expose another brand's subscriber.
	exec("INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,first_opened_at,unsubscribed_at,bounced_at,complained_at) VALUES(1,99,'sent',99999,99999,99999,99999)")
	path := fmt.Sprintf("/api/v1/campaigns/1/report?brand_id=%d", brand.ID)
	w := request(mux, "GET", path, "", &cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Recent map[string][]struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
			At    int64  `json:"at"`
		} `json:"recent"`
		Delivery struct {
			First    int64  `json:"first_sent_at"`
			Last     int64  `json:"last_sent_at"`
			From     string `json:"from_email"`
			Snapshot bool   `json:"sender_snapshotted"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for i, event := range campaignRecentEvents {
		rows := out.Recent[event.name]
		if len(rows) != 10 {
			t.Fatalf("%s: %v", event.name, rows)
		}
		for j, row := range rows {
			wantID := int64(12 - j)
			if row.ID != wantID || row.At != int64(2000+i*1000)+wantID || row.Email != fmt.Sprintf("sent%d@example.org", wantID) || row.Name != "Snapshot name" {
				t.Fatalf("%s row %d: %+v", event.name, j, row)
			}
		}
		plan, err := queryRows(context.Background(), db.Reader, "EXPLAIN QUERY PLAN "+campaignRecentSQL(event.column), 1, brand.ID)
		if err != nil {
			t.Fatal(err)
		}
		details := fmt.Sprint(plan)
		if !strings.Contains(details, "campaign_recipients_recent_"+event.name) || strings.Contains(details, "TEMP B-TREE") {
			t.Fatal("unbounded sample query", details)
		}
	}
	if out.Delivery.First != 1001 || out.Delivery.Last != 1012 || out.Delivery.From != "historical@example.org" || !out.Delivery.Snapshot {
		t.Fatal(out.Delivery)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1/report?brand_id=%d", other.ID), "", &cookie, "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Preview restores sent preparation and neither edits content nor creates delivery state.
	var before int64
	if err := db.Writer.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1/preview?brand_id=%d", brand.ID), "", &cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Saved") {
		t.Fatal(w.Code, w.Body.String())
	}
	var after int64
	if err := db.Writer.QueryRow("SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("preview modified database", before, after, err)
	}
	var count int
	if err := db.Reader.QueryRow("SELECT count(*) FROM campaign_preparations").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/2/report?brand_id=%d", brand.ID), "", &cookie, "")
	var empty struct {
		Delivery map[string]any   `json:"delivery"`
		Recent   map[string][]any `json:"recent"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if empty.Delivery["first_sent_at"] != nil || empty.Delivery["last_sent_at"] != nil || empty.Delivery["sender_snapshotted"] != false || len(empty.Recent["opened"]) != 1 {
		t.Fatal(w.Body.String())
	}
}

func TestImportedCampaignReportUsesArchivedSendDate(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Imported", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Writer.Exec(`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at,started_at) VALUES(1,?,'45db6466c59b555680b70b4dd505ae8a','sent','Imported','',1,1,1788254101000);
 INSERT INTO migration_identities(source,entity,source_id,target_id) VALUES('sendy','campaigns','39',1);
 INSERT INTO migration_records(source,entity,source_id,payload) VALUES('sendy','campaigns','39','{"sent":"1788254101"}');`, brand.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1/report?brand_id=%d", brand.ID), "", &cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Delivery struct {
			First      int64  `json:"first_sent_at"`
			Last       *int64 `json:"last_sent_at"`
			Historical bool   `json:"historical"`
			Estimated  bool   `json:"delivered_estimated"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Delivery.First != 1788254101000 || out.Delivery.Last != nil || !out.Delivery.Historical || !out.Delivery.Estimated {
		t.Fatalf("delivery: %+v", out.Delivery)
	}
	// An explicit delivery counter must not be labelled estimated.
	if _, err := db.Writer.Exec(`UPDATE migration_records SET payload=json_set(payload,'$.delivered','100')`); err != nil {
		t.Fatal(err)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1/report?brand_id=%d", brand.ID), "", &cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Delivery.Estimated {
		t.Fatal("explicit delivery counter labelled estimated")
	}
}

func TestNewCampaignReportIgnoresReusedMigrationID(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Report", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Writer.Exec(`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,'45db6466c59b555680b70b4dd505ae8a','sent','Imported','',1,1);
 INSERT INTO migration_identities(source,entity,source_id,target_id) VALUES('sendy','campaigns','39',1);
 INSERT INTO migration_records(source,entity,source_id,payload) VALUES('sendy','campaigns','39','{"sent":"1788254101"}');`, brand.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/campaigns/1?brand_id=%d", brand.ID)
	w := request(mux, "DELETE", path, "", &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "POST", fmt.Sprintf("/api/v1/campaigns?brand_id=%d", brand.ID), `{"subject":"New newsletter","html":"<p>New</p>"}`, &cookie, csrf)
	var created struct{ ID int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != 200 || created.ID != 1 {
		t.Fatal("expected reused campaign ID", w.Code, w.Body.String(), err)
	}
	for _, prepared := range []bool{false, true} {
		if prepared {
			_, err = db.Writer.Exec(`UPDATE campaigns SET status='sent',started_at=1234 WHERE id=1;
 INSERT INTO campaign_preparations(campaign_id,subject,html,text,from_name,from_email,tracking_url,prepared_at) VALUES(1,'New newsletter','<p>New</p>','','Sender','from@example.org','https://example.org',1234);`)
			if err != nil {
				t.Fatal(err)
			}
		}
		w = request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1/report?brand_id=%d", brand.ID), "", &cookie, "")
		var out struct {
			Delivery struct {
				Historical bool   `json:"historical"`
				Estimated  bool   `json:"delivered_estimated"`
				First      *int64 `json:"first_sent_at"`
				Snapshot   bool   `json:"sender_snapshotted"`
			} `json:"delivery"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
			t.Fatal(w.Code, w.Body.String(), err)
		}
		if out.Delivery.Historical || out.Delivery.Estimated || out.Delivery.First != nil || out.Delivery.Snapshot != prepared {
			t.Fatalf("native campaign mistaken for import (prepared=%v): %+v", prepared, out.Delivery)
		}
	}
}
