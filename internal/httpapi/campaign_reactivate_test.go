package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"postbox/internal/subscribers"
)

func TestCampaignReactivationAPI(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	for _, name := range []string{"one", "two"} {
		if _, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Writer.Exec(`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'reactivate','cancelled','Hello','<p>Hello</p>',1,1)`); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/campaigns/1/reactivate?brand_id="
	if w := request(mux, "POST", path+"2", "", &cookie, csrf); w.Code != http.StatusNotFound {
		t.Fatalf("cross-brand reactivation: %d %s", w.Code, w.Body.String())
	}
	w := request(mux, "GET", "/api/v1/campaigns/1/report?brand_id=1", "", &cookie, csrf)
	var report struct {
		CanReactivate bool `json:"can_reactivate"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); w.Code != http.StatusOK || err != nil || !report.CanReactivate {
		t.Fatalf("report eligibility: %d %s err=%v", w.Code, w.Body.String(), err)
	}
	if w := request(mux, "POST", path+"1", "", &cookie, csrf); w.Code != http.StatusOK {
		t.Fatalf("reactivation: %d %s", w.Code, w.Body.String())
	}
	if w := request(mux, "POST", path+"1", "", &cookie, csrf); w.Code != http.StatusConflict {
		t.Fatalf("draft reactivation: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Writer.Exec(`UPDATE campaigns SET status='cancelled' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`INSERT INTO campaign_stats(campaign_id,sent) VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	if w := request(mux, "POST", path+"1", "", &cookie, csrf); w.Code != http.StatusConflict {
		t.Fatalf("sent reactivation: %d %s", w.Code, w.Body.String())
	}
}
