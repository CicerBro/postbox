package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"postbox/internal/subscribers"
)

func TestCampaignRecoveryCombinesRecipientsAndPreservesSendReports(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	brands := subscribers.New(db)
	brand, err := brands.CreateBrand(context.Background(), subscribers.Brand{Name: "Recovery", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := brands.CreateBrand(context.Background(), subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Writer.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ id, brand int64 }{{1, brand.ID}, {21, brand.ID}, {22, other.ID}} {
		exec("INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,?,?,'sent','Black Friday','<p>Saved</p>',1,1)", c.id, c.brand, fmt.Sprint(c.id))
		exec("INSERT INTO campaign_preparations(campaign_id,subject,html,text,from_name,from_email,tracking_url,prepared_at,recipients_snapshotted) VALUES(?,'Black Friday','<p>Saved</p>','','Sender','from@example.org','https://example.org',1,1)", c.id)
	}
	for id := 1; id <= 4; id++ {
		exec("INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(?,?,?,1,1)", id, brand.ID, fmt.Sprintf("reader%d@example.org", id))
	}
	exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,sent_at,delivered_at,bounced_at,first_opened_at,first_clicked_at)
		VALUES(1,1,'sent',10,NULL,20,25,27),(21,1,'sent',30,40,NULL,45,46),
		(1,2,'sent',10,11,20,NULL,NULL),(21,2,'sent',30,NULL,NULL,NULL,NULL),
		(1,3,'sent',10,11,NULL,12,NULL),(21,4,'sent',30,NULL,NULL,NULL,NULL)`)
	exec("INSERT INTO campaign_stats(campaign_id,recipients,sent,delivered,bounces_soft) VALUES(1,3,3,2,2),(21,3,3,1,0)")

	path := func(endpoint string) string {
		return fmt.Sprintf("/api/v1/campaigns%s?brand_id=%d", endpoint, brand.ID)
	}
	for _, link := range []struct{ child, parent, status int }{
		{21, 22, http.StatusConflict}, {21, 21, http.StatusBadRequest},
		{21, 1, http.StatusOK}, {21, 1, http.StatusOK}, {1, 21, http.StatusConflict},
	} {
		w := request(mux, "PUT", path(fmt.Sprintf("/%d/recovery", link.child)), fmt.Sprintf(`{"original_campaign_id":%d}`, link.parent), &cookie, csrf)
		if w.Code != link.status {
			t.Fatalf("link %+v: %d %s", link, w.Code, w.Body.String())
		}
	}
	var report struct {
		Stats    []struct{ Sent, Delivered int }
		Recovery struct {
			Original int64 `json:"original_campaign_id"`
			Sends    []struct{ ID int }
			Stats    campaignRecoveryStats
		}
	}
	getReport := func(id int) {
		t.Helper()
		w := request(mux, "GET", path(fmt.Sprintf("/%d/report", id)), "", &cookie, "")
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
	}
	getReport(1)
	want := campaignRecoveryStats{Recipients: 4, Sent: 4, Delivered: 2, Bounced: 1, AwaitingOutcome: 1, Recovered: 1, OpensUnique: 2, ClicksUnique: 1}
	if report.Recovery.Original != 1 || len(report.Recovery.Sends) != 2 ||
		report.Recovery.Sends[0].ID != 1 || report.Recovery.Sends[1].ID != 21 ||
		report.Recovery.Stats != want || len(report.Stats) != 1 || report.Stats[0].Delivered != 2 {
		t.Fatalf("original report: %+v", report)
	}
	report.Recovery.Sends = nil
	getReport(21)
	if report.Recovery.Original != 1 || len(report.Recovery.Sends) != 0 || report.Stats[0].Sent != 3 || report.Stats[0].Delivered != 1 {
		t.Fatalf("recovery report: %+v", report)
	}

	for _, query := range []string{"&summary=true", "&summary=true&offset=0"} {
		w := request(mux, "GET", path("")+query, "", &cookie, "")
		var rows []struct {
			ID            int
			RecoveryCount int `json:"recovery_count"`
			Stats         campaignRecoveryStats
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rows); w.Code != http.StatusOK || err != nil ||
			len(rows) != 1 || rows[0].ID != 1 || rows[0].RecoveryCount != 1 || rows[0].Stats != want {
			t.Fatalf("campaign summary: %d %s (%v)", w.Code, w.Body.String(), err)
		}
	}

	exec("UPDATE campaign_recipients SET delivered_at=50 WHERE campaign_id=21 AND subscriber_id=2")
	getReport(1)
	if stats := report.Recovery.Stats; stats.Delivered != 3 || stats.Bounced != 0 || stats.Recovered != 2 {
		t.Fatalf("live recovery stats: %+v", stats)
	}
}
