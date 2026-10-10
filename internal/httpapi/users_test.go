package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/pquerna/otp/totp"
	"net/http"
	"postbox/internal/send"
	"sync"
	"testing"
	"time"
)

func TestUserManagementAuthorizationAndMFA(t *testing.T) {
	a, m, db, c, csrf := setup(t)
	// Explicit registration also permits running this suite before coordinator wiring.
	if _, pattern := m.Handler(newTestRequest("GET", "/api/v1/users")); pattern == "" {
		a.registerUsers(m)
	}
	w := request(m, "POST", "/api/v1/users", `{"email":"client@example.org","password":"client-long-password"}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	login := request(m, "POST", "/api/v1/auth/login", `{"email":"client@example.org","password":"client-long-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	client := *login.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &session)
	if w = request(m, "GET", "/api/v1/users", "", &client, ""); w.Code != 403 {
		t.Fatal("user listing escaped admin boundary", w.Code)
	}
	if w = request(m, "PATCH", fmt.Sprintf("/api/v1/users/%d", created.ID), `{"role":"admin"}`, &client, session.CSRF); w.Code != 403 {
		t.Fatal("user self promoted", w.Code)
	}
	if w = request(m, "DELETE", "/api/v1/users/1", "", &c, csrf); w.Code != 400 {
		t.Fatal("last administrator deleted", w.Code)
	}
	w = request(m, "POST", "/api/v1/auth/totp/enroll", `{"password":"client-long-password"}`, &client, session.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var enrollment struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(w.Body.Bytes(), &enrollment)
	var enabled bool
	if err := db.Reader.QueryRow(`SELECT totp_secret IS NOT NULL FROM users WHERE id=?`, created.ID).Scan(&enabled); err != nil || enabled {
		t.Fatal("unverified secret enabled", err)
	}
	if w = request(m, "POST", "/api/v1/auth/totp/verify", `{"code":"bad"}`, &client, session.CSRF); w.Code != 400 {
		t.Fatal("bad MFA challenge accepted", w.Code)
	}
	code, _ := totp.GenerateCode(enrollment.Secret, time.Now())
	w = request(m, "POST", "/api/v1/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, code), &client, session.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "POST", "/api/v1/auth/login", `{"email":"client@example.org","password":"client-long-password"}`, nil, ""); w.Code != 401 {
		t.Fatal("MFA bypassed", w.Code)
	}
	if w = request(m, "DELETE", "/api/v1/auth/totp", `{"password":"client-long-password","code":"bad"}`, &client, session.CSRF); w.Code != 400 {
		t.Fatal("MFA removed without code", w.Code)
	}
	if w = request(m, "DELETE", "/api/v1/auth/totp", fmt.Sprintf(`{"password":"client-long-password","code":%q}`, code), &client, session.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "GET", "/api/v1/auth/session", "", &client, ""); w.Code != 401 {
		t.Fatal("MFA removal kept old session", w.Code)
	}
}
func newTestRequest(method, path string) *http.Request {
	r, _ := http.NewRequest(method, path, nil)
	return r
}

func TestDailyReservationIsAtomicAndIdempotent(t *testing.T) {
	_, _, db, _, _ := setup(t)
	hash, err := HashPassword("client-long-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'b','b','a@example.org',0)`, fmt.Sprintf(`INSERT INTO users(id,email,password_hash,role,created_at) VALUES(2,'client@example.org','%s','brand_user',0)`, hash), `INSERT INTO user_brands(user_id,brand_id,permissions,daily_send_limit) VALUES(2,1,'["read","send"]',10)`, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at,created_by) VALUES(1,1,'a','draft','s','h',0,0,2),(2,1,'b','draft','s','h',0,0,2)`} {
		if _, err := db.Writer.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []int64{1, 2} {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); errs <- send.ReserveUserCampaign(context.Background(), db, id, 6) }(id)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected one budget winner got %d", success)
	}
	var id int64
	if err := db.Reader.QueryRow(`SELECT campaign_id FROM campaign_user_reservations`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := send.ReserveUserCampaign(context.Background(), db, id, 6); err != nil {
		t.Fatal("restart double charged", err)
	}
	var used int64
	if err := db.Reader.QueryRow(`SELECT reserved FROM user_brand_daily`).Scan(&used); err != nil || used != 6 {
		t.Fatal("invalid budget", used, err)
	}
	if _, err := db.Writer.Exec(`UPDATE user_brands SET permissions='["read"]'`); err != nil {
		t.Fatal(err)
	}
	if err := send.ReserveUserCampaign(context.Background(), db, id, 6); err == nil {
		t.Fatal("revoked sender restarted campaign")
	}
}

func TestBrandUserPermissionsAndAttribution(t *testing.T) {
	_, m, db, c, csrf := setup(t)
	for _, name := range []string{"one", "two"} {
		w := request(m, "POST", "/api/v1/brands", fmt.Sprintf(`{"name":%q,"from_email":"from@example.org"}`, name), &c, csrf)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(m, "POST", "/api/v1/users", `{"email":"limited@example.org","password":"limited-long-password"}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var user struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &user)
	member := fmt.Sprintf("/api/v1/users/%d/brands/1", user.ID)
	w = request(m, "PUT", member, `{"permissions":["read"],"daily_send_limit":3}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(m, "POST", "/api/v1/auth/login", `{"email":"limited@example.org","password":"limited-long-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	client := *w.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &session)
	w = request(m, "GET", "/api/v1/brands", "", &client, "")
	var brands []map[string]any
	json.Unmarshal(w.Body.Bytes(), &brands)
	if w.Code != 200 || len(brands) != 1 || brands[0]["name"] != "one" {
		t.Fatal("brand scope leaked", w.Body.String())
	}
	for _, tc := range []struct{ method, path, body string }{{"POST", "/api/v1/campaigns?brand_id=1", `{"subject":"s","html":"h"}`}, {"GET", "/api/v1/campaigns?brand_id=2", ""}, {"POST", "/api/v1/campaigns/1/start?brand_id=1", ""}, {"PUT", member, `{"permissions":["read","send"]}`}} {
		if w = request(m, tc.method, tc.path, tc.body, &client, session.CSRF); w.Code != 403 {
			t.Fatal("read-only scope escaped", tc.path, w.Code, w.Body.String())
		}
	}
	w = request(m, "PUT", member, `{"permissions":["write"]}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "GET", "/api/v1/campaigns?brand_id=1", "", &client, ""); w.Code != 403 {
		t.Fatal("write granted implicit read", w.Code)
	}
	w = request(m, "POST", "/api/v1/campaigns?brand_id=1", `{"subject":"s","html":"h"}`, &client, session.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var campaign struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &campaign)
	var creator int64
	if err := db.Reader.QueryRow(`SELECT created_by FROM campaigns WHERE id=?`, campaign.ID).Scan(&creator); err != nil || creator != user.ID {
		t.Fatal("campaign attribution missing", creator, err)
	}
	if w = request(m, "POST", fmt.Sprintf("/api/v1/campaigns/%d/start?brand_id=1", campaign.ID), "", &client, session.CSRF); w.Code != 403 {
		t.Fatal("write granted send", w.Code)
	}
	if w = request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"escape","scopes":["write"]}`, &client, session.CSRF); w.Code != 403 {
		t.Fatal("limited user minted unlimited bearer", w.Code)
	}
	w = request(m, "PUT", member, `{"permissions":["send"],"daily_send_limit":0}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := send.ReserveUserTestSend(context.Background(), db, campaign.ID, 1); err == nil {
		t.Fatal("zero send budget ignored")
	}
}
