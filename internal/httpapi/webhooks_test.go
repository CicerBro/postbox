package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"postbox/internal/subscribers"
)

func TestWebhooksCRUDValidationAndAuthorization(t *testing.T) {
	_, m, db, c, csrf := setup(t)
	service := subscribers.New(db)
	b, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Webhook", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Other", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/webhooks?brand_id=%d", b.ID)
	body := `{"url":"https://hooks.example.org/events","events":["subscribe","campaign_finished"]}`
	if w := request(m, "GET", path, "", nil, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(m, "POST", path, body, &c, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, bad := range []string{
		`{"url":"http://127.0.0.1","events":["subscribe"]}`,
		`{"url":"http://169.254.169.254/latest/meta-data","events":["subscribe"]}`,
		`{"url":"https://localhost","events":["subscribe"]}`,
		`{"url":"http://[::1]","events":["subscribe"]}`,
		`{"url":"ftp://hooks.example.org","events":["subscribe"]}`,
		`{"url":"https://user:pass@hooks.example.org","events":["subscribe"]}`,
		`{"url":"https://hooks.example.org/#fragment","events":["subscribe"]}`,
		`{"url":"https://hooks.example.org","events":["sent"]}`,
		`{"url":"https://hooks.example.org","events":[]}`,
		`{"url":"https://hooks.example.org","events":["subscribe","subscribe"]}`,
		`{"url":"https://hooks.example.org","events":["subscribe"],"secret":"chosen"}`,
		`{"url":"https://hooks.example.org","events":["subscribe"],"rotate_secret":true}`,
	} {
		if w := request(m, "POST", path, bad, &c, csrf); w.Code != 400 {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body.String())
		}
	}
	w := request(m, "POST", path, body, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created webhook
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID <= 0 || len(created.Secret) < 40 || !created.Active {
		t.Fatal(w.Body.String())
	}
	item := fmt.Sprintf("/api/v1/webhooks/%d?brand_id=%d", created.ID, b.ID)
	for _, p := range []string{path, item} {
		w = request(m, "GET", p, "", &c, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), created.Secret) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	foreign := fmt.Sprintf("/api/v1/webhooks/%d?brand_id=%d", created.ID, other.ID)
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		w = request(m, method, foreign, `{"is_active":false}`, &c, csrf)
		if w.Code != 404 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	for _, bad := range []string{`{}`, `{"events":[]}`, `{"url":"http://10.0.0.1"}`} {
		if w = request(m, "PATCH", item, bad, &c, csrf); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request(m, "PATCH", item, `{"is_active":false,"events":["bounce","complaint"],"url":"https://hooks.example.org/new"}`, &c, csrf)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	var updated webhook
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Active || len(updated.Events) != 2 || updated.URL != "https://hooks.example.org/new" {
		t.Fatal(w.Body.String())
	}
	var storedSecret string
	if err = db.Reader.QueryRow("SELECT secret FROM webhooks WHERE id=?", created.ID).Scan(&storedSecret); err != nil || storedSecret != created.Secret {
		t.Fatal(storedSecret, err)
	}
	w = request(m, "PATCH", item, `{"rotate_secret":true}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Secret == "" || updated.Secret == created.Secret {
		t.Fatal(w.Body.String())
	}
	if err = db.Reader.QueryRow("SELECT secret FROM webhooks WHERE id=?", created.ID).Scan(&storedSecret); err != nil || storedSecret != updated.Secret {
		t.Fatal(storedSecret, err)
	}

	// API keys inherit their brand and require the usual read/write scope.
	for _, scopes := range []string{`["read"]`, `["write"]`} {
		key := "hook-" + secret()[:8] + "." + secret()
		prefix, _, _ := strings.Cut(key, ".")
		_, err = db.Writer.Exec("INSERT INTO api_keys(brand_id,name,prefix,secret_hash,scopes,created_at) VALUES(?,'test',?,?,?,?)", b.ID, prefix, digest(key), scopes, time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			method, path, body string
			want               int
		}{
			{"GET", "/api/v1/webhooks", "", map[bool]int{true: 200, false: 403}[scopes == `["read"]`]},
			{"POST", "/api/v1/webhooks", body, map[bool]int{true: 403, false: 200}[scopes == `["read"]`]},
			{"GET", fmt.Sprintf("/api/v1/webhooks?brand_id=%d", other.ID), "", 403},
		} {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			m.ServeHTTP(recorder, r)
			if recorder.Code != tc.want {
				t.Fatal(scopes, tc.method, recorder.Code, recorder.Body.String())
			}
		}
	}
	// A brand user needs explicit read or write permissions and cannot cross brands.
	res, err := db.Writer.Exec("INSERT INTO users(email,password_hash,role,created_at) VALUES('hook-user@example.org','unused','brand_user',?)", time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	_, err = db.Writer.Exec("INSERT INTO user_brands(user_id,brand_id,permissions) VALUES(?,?,?)", uid, b.ID, `["read"]`)
	if err != nil {
		t.Fatal(err)
	}
	token := secret()
	userCSRF := secret()
	_, err = db.Writer.Exec("INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(?,?,?,?)", digest(token), uid, time.Now().Add(time.Hour).UnixMilli(), digest(userCSRF))
	if err != nil {
		t.Fatal(err)
	}
	cookie := http.Cookie{Name: "postbox_session", Value: token}
	if w = request(m, "GET", path, "", &cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "PATCH", item, `{"is_active":true}`, &cookie, userCSRF); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "GET", foreign, "", &cookie, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, err = db.Writer.Exec("UPDATE user_brands SET permissions='[\"write\"]' WHERE user_id=?", uid)
	if err != nil {
		t.Fatal(err)
	}
	if w = request(m, "PATCH", item, `{"is_active":true}`, &cookie, userCSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "GET", path, "", &cookie, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "DELETE", item, "", &c, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "GET", item, "", &c, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(m, "GET", "/api/v1/openapi.json", "", &c, "")
	if !strings.Contains(w.Body.String(), "rotate_secret") || !strings.Contains(w.Body.String(), "campaign_finished") {
		t.Fatal(w.Body.String())
	}
}
