package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestRecipientCSVExportSpreadsheetSafety(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "CSV", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Writer.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,'csv-export','sent','CSV','<p>CSV</p>',1,1)", brand.ID)
	profileEmail, profileName := "=formula@example.org", "\t=Profile(\"雪, café\")"
	exec("INSERT INTO subscribers(id,brand_id,email,name,created_at,updated_at) VALUES(1,?,?,?,1,1)", brand.ID, profileEmail, profileName)
	email, messageID, sendError := "+formula@example.org", " \t@message", "\ufeff\u200b=cmd(\"雪, café\")\nmore"
	exec("INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,snapshot_email,message_id,error,sent_at) VALUES(1,1,'failed',?,?,?,2)", email, messageID, sendError)
	w := request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1/export?brand_id=%d", brand.ID), "", &cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][1] != "'"+email || rows[1][3] != "'"+messageID || rows[1][4] != "'"+sendError {
		t.Fatalf("recipient CSV rows = %#v", rows)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/subscribers/export?brand_id=%d", brand.ID), "", &cookie, "")
	rows, err = csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if w.Code != 200 || err != nil || len(rows) != 2 || rows[1][0] != "'"+profileEmail || rows[1][1] != "'"+profileName {
		t.Fatalf("subscriber CSV: %d %#v %v", w.Code, rows, err)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/subscribers/1?brand_id=%d", brand.ID), "", &cookie, "")
	var raw []struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	err = json.Unmarshal(w.Body.Bytes(), &raw)
	if w.Code != 200 || err != nil || len(raw) != 1 || raw[0].Email != profileEmail || raw[0].Name != profileName {
		t.Fatalf("raw JSON values: %d %+v %v", w.Code, raw, err)
	}
}
