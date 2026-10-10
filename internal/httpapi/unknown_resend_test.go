package httpapi

import (
	"net/http"
	"testing"
)

func TestUnknownResendCannotBypassDeliverySafety(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	for _, q := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','from@example.org',1)`,
		`INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'reader@example.org',1,1)`,
		`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'test','sent','Subject','Body',1,1)`,
		`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,attempts) VALUES(1,1,'unknown',1)`,
		`INSERT INTO campaign_stats(campaign_id,recipients,unknown) VALUES(1,1,1)`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	w := request(mux, "POST", "/api/v1/campaigns/1/resend-unknown?brand_id=1", "{}", &cookie, csrf)
	if w.Code != http.StatusConflict {
		t.Fatalf("unsafe resend response %d: %s", w.Code, w.Body.String())
	}
	var state string
	if err := db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "unknown" {
		t.Fatalf("unknown recipient requeued: %s", state)
	}
}
