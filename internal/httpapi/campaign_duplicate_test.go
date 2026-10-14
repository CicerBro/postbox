package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"postbox/internal/subscribers"
	"testing"
)

func TestDuplicateCampaignCopiesSavedSetupAsNewDraft(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	for _, name := range []string{"one", "two"} {
		if _, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"draft", "sent", "scheduled"} {
		t.Run(status, func(t *testing.T) {
			res, err := db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,text,inline_css,scheduled_at,started_at,finished_at,created_at,updated_at) VALUES(1,?,?,?, ?,?,0,123,456,789,1,1)", "source-"+status, status, "Subject "+status, "<p>Content</p>", "Plain text")
			if err != nil {
				t.Fatal(err)
			}
			id, _ := res.LastInsertId()
			for _, target := range []struct {
				kind string
				id   int
				mode string
			}{{"list", 10, "include"}, {"segment", 20, "exclude"}} {
				if _, err := db.Writer.Exec("INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(?,?,?,?)", id, target.kind, target.id, target.mode); err != nil {
					t.Fatal(err)
				}
			}
			path := fmt.Sprintf("/api/v1/campaigns/%d/duplicate?brand_id=", id)
			if w := request(mux, "POST", path+"2", "", &cookie, csrf); w.Code != http.StatusNotFound {
				t.Fatalf("cross-brand duplicate: %d %s", w.Code, w.Body.String())
			}
			w := request(mux, "POST", path+"1", "", &cookie, csrf)
			if w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			var copy struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &copy); err != nil || copy.ID == 0 || copy.ID == id {
				t.Fatalf("invalid copy response: %s %v", w.Body.String(), err)
			}
			var brand int64
			var uuid, copiedStatus, subject, html, plain string
			var inline int
			var scheduled, started, finished any
			if err := db.Reader.QueryRow("SELECT brand_id,uuid,status,subject,html,text,inline_css,scheduled_at,started_at,finished_at FROM campaigns WHERE id=?", copy.ID).Scan(&brand, &uuid, &copiedStatus, &subject, &html, &plain, &inline, &scheduled, &started, &finished); err != nil {
				t.Fatal(err)
			}
			if brand != 1 || uuid == "source-"+status || copiedStatus != "draft" || subject != "Subject "+status || html != "<p>Content</p>" || plain != "Plain text" || inline != 0 || scheduled != nil || started != nil || finished != nil {
				t.Fatalf("copy did not preserve the setup and reset delivery state: brand=%d uuid=%q status=%q subject=%q html=%q text=%q inline=%d schedule=%v started=%v finished=%v", brand, uuid, copiedStatus, subject, html, plain, inline, scheduled, started, finished)
			}
			rows, err := db.Reader.Query("SELECT target_type,target_id,mode FROM campaign_targets WHERE campaign_id=? ORDER BY target_type", copy.ID)
			if err != nil {
				t.Fatal(err)
			}
			var targets []string
			for rows.Next() {
				var kind, mode string
				var targetID int
				if err := rows.Scan(&kind, &targetID, &mode); err != nil {
					t.Fatal(err)
				}
				targets = append(targets, fmt.Sprintf("%s:%d:%s", kind, targetID, mode))
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(targets) != 2 || targets[0] != "list:10:include" || targets[1] != "segment:20:exclude" {
				t.Fatalf("copied targets: %v", targets)
			}
		})
	}
}
