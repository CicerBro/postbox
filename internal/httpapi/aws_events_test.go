package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"postbox/internal/awsbrand"
	"postbox/internal/subscribers"
)

func TestAWSEventSetupAccessValidationAndRedaction(t *testing.T) {
	a, m, db, c, csrf := setup(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "PRIVATE-SECRET AKIAEVENT", 403) }))
	defer upstream.Close()
	p, err := awsbrand.New(db, []byte(strings.Repeat("x", 32)), "eu-west-1", upstream.URL, 1)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.AWSProvider = p
	for _, name := range []string{"one", "two"} {
		if _, err = subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.Save(context.Background(), 1, awsbrand.Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "AKIAEVENT", SecretAccessKey: "PRIVATE-SECRET"}); err != nil {
		t.Fatal(err)
	}
	setupPath := "/api/v1/aws-settings/setup-events?brand_id=1"
	statusPath := "/api/v1/aws-settings/event-status?brand_id=1"
	if w := request(m, "POST", setupPath, "", &c, ""); w.Code != 403 {
		t.Fatal("missing CSRF allowed", w.Code)
	}
	a.cfg.BaseURL = "http://localhost:8080"
	if w := request(m, "POST", setupPath, "", &c, csrf); w.Code != 400 || !strings.Contains(w.Body.String(), "public HTTPS") {
		t.Fatal(w.Code, w.Body.String())
	}
	a.cfg.BaseURL = "https://postbox.example.org"
	// No saved resources means status is returned without contacting AWS.
	w := request(m, "GET", statusPath, "", &c, "")
	var status awsbrand.EventStatus
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if w.Code != 200 || status.SubscriptionStatus != "not_subscribed" || status.Configured || status.Endpoint != "https://postbox.example.org/events/sns" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(m, "POST", setupPath, "", &c, csrf); w.Code != 502 || strings.Contains(w.Body.String(), "PRIVATE-SECRET") || strings.Contains(w.Body.String(), "AKIAEVENT") {
		t.Fatal("upstream leak", w.Code, w.Body.String())
	}
	w = request(m, "POST", "/api/v1/users", `{"email":"events-reader@example.org","password":"reader-password"}`, &c, csrf)
	var user struct{ ID int64 }
	_ = json.Unmarshal(w.Body.Bytes(), &user)
	if w = request(m, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/1", user.ID), `{"permissions":["read"]}`, &c, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	login := request(m, "POST", "/api/v1/auth/login", `{"email":"events-reader@example.org","password":"reader-password"}`, nil, "")
	member := login.Result().Cookies()[0]
	var sess struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(login.Body.Bytes(), &sess)
	if w = request(m, "GET", statusPath, "", member, ""); w.Code != 200 {
		t.Fatal("reader status denied", w.Code, w.Body.String())
	}
	if w = request(m, "GET", "/api/v1/aws-settings/event-status?brand_id=2", "", member, ""); w.Code != 403 {
		t.Fatal("cross-brand status allowed", w.Code)
	}
	if w = request(m, "POST", setupPath, "", member, sess.CSRF); w.Code != 403 {
		t.Fatal("reader created resources", w.Code)
	}
	w = request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"events","scopes":["read","write"]}`, &c, csrf)
	var key struct{ Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &key)
	for _, route := range []struct{ method, path string }{{"GET", statusPath}, {"POST", setupPath}} {
		r := httptest.NewRequest(route.method, route.path, nil)
		r.Header.Set("Authorization", "Bearer "+key.Secret)
		recorder := httptest.NewRecorder()
		m.ServeHTTP(recorder, r)
		if recorder.Code != 403 {
			t.Fatal("API key accessed AWS events", recorder.Code)
		}
	}
	a.cfg.BaseURL = "http://localhost:8080"
	_ = request(m, "POST", setupPath, "", &c, csrf)
	if w = request(m, "POST", setupPath, "", &c, csrf); w.Code != 429 {
		t.Fatal("setup not rate limited", w.Code)
	}
}
