package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"postbox/internal/subscribers"
)

func TestCampaignScheduleCanBeUpdatedBeforePreparation(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	for _, name := range []string{"one", "two"} {
		if _, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	execStreamSQL(t, db, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'schedule','draft','Hello','<p>Hello</p>',1,1)`)
	path := "/api/v1/campaigns/1/schedule?brand_id=1"
	var savedAt int64
	assertSchedule := func(status string, at int64) {
		t.Helper()
		var gotStatus, subject, html string
		var gotAt int64
		if err := db.Reader.QueryRow(`SELECT status,scheduled_at,subject,html FROM campaigns WHERE id=1`).Scan(&gotStatus, &gotAt, &subject, &html); err != nil {
			t.Fatal(err)
		}
		if gotStatus != status || gotAt != at || subject != "Hello" || html != "<p>Hello</p>" {
			t.Fatalf("campaign changed unexpectedly: %s %d %q %q", gotStatus, gotAt, subject, html)
		}
	}
	for _, hours := range []int{2, 4, 1} {
		savedAt = time.Now().Add(time.Duration(hours) * time.Hour).UnixMilli()
		body := fmt.Sprintf(`{"scheduled_at":%d,"timezone":"Asia/Kathmandu"}`, savedAt)
		if w := request(mux, "POST", path, body, &cookie, csrf); w.Code != http.StatusOK {
			t.Fatalf("schedule/update: %d %s", w.Code, w.Body.String())
		}
		assertSchedule("scheduled", savedAt)
	}
	for _, body := range []string{`{}`, `{"scheduled_at":0}`, `{"scheduled_at":-1}`, fmt.Sprintf(`{"scheduled_at":%d,"timezone":"invalid"}`, savedAt)} {
		if w := request(mux, "POST", path, body, &cookie, csrf); w.Code < 400 {
			t.Fatalf("invalid schedule accepted: %s", body)
		}
		assertSchedule("scheduled", savedAt)
	}
	body := fmt.Sprintf(`{"scheduled_at":%d}`, savedAt+3600000)
	if w := request(mux, "POST", "/api/v1/campaigns/1/schedule?brand_id=2", body, &cookie, csrf); w.Code != http.StatusNotFound {
		t.Fatalf("cross-brand schedule: %d %s", w.Code, w.Body.String())
	}
	if w := request(mux, "POST", "/api/v1/campaigns/1/start?brand_id=1", `{}`, &cookie, csrf); w.Code != http.StatusConflict {
		t.Fatalf("start overwrote schedule: %d %s", w.Code, w.Body.String())
	}
	assertSchedule("scheduled", savedAt)
	for _, status := range []string{"preparing", "sending", "paused", "sent", "cancelled"} {
		execStreamSQL(t, db, `UPDATE campaigns SET status=? WHERE id=1`, status)
		if w := request(mux, "POST", path, body, &cookie, csrf); w.Code != http.StatusConflict {
			t.Fatalf("reschedule %s: %d %s", status, w.Code, w.Body.String())
		}
		assertSchedule(status, savedAt)
	}
}

func TestReschedulingRequiresSendPermissionAndCampaignOwnership(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	streamCampaignFixture(t, db)
	execStreamSQL(t, db, `UPDATE users SET role='brand_user' WHERE id=1`)
	execStreamSQL(t, db, `INSERT INTO user_brands(user_id,brand_id,permissions) VALUES(1,1,'["read","write","send"]')`)
	execStreamSQL(t, db, `UPDATE campaigns SET status='scheduled',scheduled_at=?,created_by=1 WHERE id=1`, time.Now().Add(time.Hour).UnixMilli())
	body := fmt.Sprintf(`{"scheduled_at":%d}`, time.Now().Add(2*time.Hour).UnixMilli())
	path := "/api/v1/campaigns/1/schedule?brand_id=1"
	if w := request(mux, "POST", path, body, &cookie, csrf); w.Code != http.StatusOK {
		t.Fatalf("owner cannot reschedule: %d %s", w.Code, w.Body.String())
	}
	execStreamSQL(t, db, `UPDATE user_brands SET permissions='["read","write"]'`)
	if w := request(mux, "POST", path, body, &cookie, csrf); w.Code != http.StatusForbidden {
		t.Fatalf("reschedule without send permission: %d %s", w.Code, w.Body.String())
	}
	execStreamSQL(t, db, `UPDATE user_brands SET permissions='["read","write","send"]'`)
	execStreamSQL(t, db, `UPDATE campaigns SET created_by=NULL WHERE id=1`)
	if w := request(mux, "POST", path, body, &cookie, csrf); w.Code != http.StatusForbidden {
		t.Fatalf("reschedule without ownership: %d %s", w.Code, w.Body.String())
	}
}
