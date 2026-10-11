package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"postbox/internal/subscribers"
	"postbox/internal/tracking"
	"postbox/internal/tracking/token"
)

func TestPublicSubscriptionSwitches(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	lists := make([]subscribers.List, 2)
	for i := range lists {
		brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: fmt.Sprintf("Brand %d", i), FromEmail: "sender@example.org"})
		if err != nil {
			t.Fatal(err)
		}
		lists[i], err = service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers", OptIn: "double"})
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := service.Upsert(ctx, subscribers.UpsertInput{BrandID: lists[0].BrandID, Email: "confirm@example.org", ListIDs: []int64{lists[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	set := func(path string, enabled bool) {
		t.Helper()
		w := request(mux, "PATCH", path, fmt.Sprintf(`{"public_subscriptions_enabled":%t}`, enabled), &cookie, csrf)
		if w.Code != 200 {
			t.Fatalf("save %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	check := func(list subscribers.List, enabled bool) {
		t.Helper()
		snapshot := func() [4]int {
			t.Helper()
			var counts [4]int
			if err := db.Reader.QueryRow(`SELECT (SELECT count(*) FROM subscribers), (SELECT count(*) FROM jobs), (SELECT count(*) FROM subscription_confirmations), (SELECT count(*) FROM subscriber_activity)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
				t.Fatal(err)
			}
			return counts
		}
		before := snapshot()
		want := http.StatusForbidden
		if enabled {
			want = http.StatusOK
		}
		for _, method := range []string{"GET", "POST"} {
			form := url.Values{"email": {fmt.Sprintf("signup-%d@example.org", list.ID)}, "consent": {"on"}}
			r := httptest.NewRequest(method, "/subscribe/"+list.UUID, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("%s list %d: got %d want %d: %s", method, list.ID, w.Code, want, w.Body.String())
			}
		}
		path := fmt.Sprintf("/api/v1/lists/%d/form?brand_id=%d", list.ID, list.BrandID)
		if w := request(mux, "GET", path, "", &cookie, csrf); w.Code != want {
			t.Fatalf("form: got %d want %d: %s", w.Code, want, w.Body.String())
		}
		if !enabled {
			if after := snapshot(); after != before {
				t.Fatalf("disabled signup changed subscribers/jobs/confirmations/activity: %v to %v", before, after)
			}
		}
	}
	brandPath := fmt.Sprintf("/api/v1/subscription-settings?brand_id=%d", lists[0].BrandID)
	w := request(mux, "GET", brandPath, "", &cookie, csrf)
	var defaults subscriptionSettings
	if json.Unmarshal(w.Body.Bytes(), &defaults) != nil || !defaults.Enabled || !defaults.ApplicationEnabled || !defaults.PublicSubscriptionsEnabled {
		t.Fatalf("default switches: %d %s", w.Code, w.Body.String())
	}
	check(lists[0], true)
	set(brandPath, false)
	check(lists[0], false)
	check(lists[1], true)
	set("/api/v1/application-settings", false)
	set(brandPath, true)
	check(lists[0], false)
	check(lists[1], false)
	w = request(mux, "GET", "/confirm/"+pending.ConfirmationTokens[lists[0].ID], "", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("confirmation disabled: %d %s", w.Code, w.Body.String())
	}
	secret := []byte(strings.Repeat("t", 32))
	tracker := tracking.New(db, &tracking.Config{TokenSecret: secret, BaseURL: "https://track.example.org"})
	tracker.Register(mux)
	if _, err = db.Writer.Exec(`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,'test-campaign','sent','Test','<p>Test</p>',1,1)`, lists[0].BrandID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,?,'sent')`, pending.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://track.example.org/u/"+token.Encode(secret, 1, pending.ID), strings.NewReader("list=all"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("unsubscribe disabled: %d %s", w.Code, w.Body.String())
	}
	set("/api/v1/application-settings", true)
	check(lists[0], true)
	check(lists[1], true)
}

func TestSubscriptionSettingsAuthorizationAndValidation(t *testing.T) {
	_, mux, db, admin, csrf := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	for _, name := range []string{"Allowed", "Other"} {
		if _, err := service.CreateBrand(ctx, subscribers.Brand{Name: name, FromEmail: "sender@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	w := request(mux, "POST", "/api/v1/users", `{"email":"member@example.org","password":"member-long-password"}`, &admin, csrf)
	var user struct{ ID int64 }
	if json.Unmarshal(w.Body.Bytes(), &user) != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	memberPath := fmt.Sprintf("/api/v1/users/%d/brands/1", user.ID)
	w = request(mux, "PUT", memberPath, `{"permissions":["read"]}`, &admin, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "POST", "/api/v1/auth/login", `{"email":"member@example.org","password":"member-long-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	member := *w.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	const brandPath = "/api/v1/subscription-settings?brand_id=1"
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", brandPath, "", 200},
		{"PATCH", brandPath, `{"public_subscriptions_enabled":false}`, 403},
		{"GET", "/api/v1/subscription-settings?brand_id=2", "", 403},
		{"GET", "/api/v1/application-settings", "", 403},
		{"PATCH", "/api/v1/application-settings", `{"public_subscriptions_enabled":false}`, 403},
	} {
		w = request(mux, tc.method, tc.path, tc.body, &member, session.CSRF)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	w = request(mux, "PUT", memberPath, `{"permissions":["read","write"]}`, &admin, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "PATCH", brandPath, `{"public_subscriptions_enabled":false}`, &member, session.CSRF)
	if w.Code != 200 {
		t.Fatalf("brand writer denied: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"public_subscriptions_enabled":null}`, `{"public_subscriptions_enabled":"true"}`, `{"public_subscriptions_enabled":true,"application_enabled":true}`} {
		w = request(mux, "PATCH", brandPath, body, &admin, csrf)
		if w.Code != 400 {
			t.Fatalf("invalid switch accepted %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w = request(mux, "PATCH", brandPath, `{"public_subscriptions_enabled":true}`, &admin, "invalid")
	if w.Code != 403 {
		t.Fatal("CSRF check bypassed", w.Code)
	}
	settings, err := db.Setting(ctx, "brand.1."+publicSubscriptionsKey)
	if err != nil || settings != "false" {
		t.Fatal("invalid request changed settings", settings, err)
	}
	w = request(mux, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"Brand signup","scopes":["read","write"]}`, &admin, csrf)
	var key struct {
		Secret string `json:"secret"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &key); err != nil || w.Code != 200 {
		t.Fatal("create API key", w.Code, w.Body.String(), err)
	}
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/v1/subscription-settings", "", 200},
		{"PATCH", "/api/v1/subscription-settings", `{"public_subscriptions_enabled":true}`, 200},
		{"PATCH", "/api/v1/subscription-settings?brand_id=2", `{"public_subscriptions_enabled":true}`, 403},
		{"GET", "/api/v1/application-settings", "", 403},
		{"PATCH", "/api/v1/application-settings", `{"public_subscriptions_enabled":false}`, 403},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+key.Secret)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("API key %s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestPublicSignupCannotOverwriteConfirmedProfile(t *testing.T) {
	_, mux, db, _, _ := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Brand", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers", OptIn: "double"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.Upsert(ctx, subscribers.UpsertInput{BrandID: brand.ID, Email: "reader@example.org", Name: "Verified reader", Fields: map[string]any{"company": "Original"}, ListIDs: []int64{list.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Confirm(ctx, profile.ConfirmationTokens[list.ID]); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"email": {profile.Email}, "name": {"Unverified replacement"}, "consent": {"on"}}
	r := httptest.NewRequest("POST", "/subscribe/"+list.UUID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var name, fields, status string
	if err = db.Reader.QueryRow(`SELECT s.name,s.fields,ls.status FROM subscribers s JOIN list_subscribers ls ON ls.subscriber_id=s.id WHERE s.id=? AND ls.list_id=?`, profile.ID, list.ID).Scan(&name, &fields, &status); err != nil {
		t.Fatal(err)
	}
	if name != "Verified reader" || fields != `{"company":"Original"}` || status != "subscribed" {
		t.Fatalf("anonymous request changed confirmed profile: %q %s %q", name, fields, status)
	}
}
