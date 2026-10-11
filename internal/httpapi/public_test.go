package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"postbox/internal/htmlpolicy"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestHostedThankyouAndJSONNegotiation(t *testing.T) {
	a, m, db, cookie, csrf := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	brand, e := service.CreateBrand(ctx, subscribers.Brand{Name: "Hosted", FromEmail: "sender@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	list, e := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers", OptIn: "double"})
	if e != nil {
		t.Fatal(e)
	}
	a.cfg.TurnstileSiteKey = "test-site-key"
	signup := request(m, "GET", "/subscribe/"+list.UUID, "", nil, "")
	if signup.Code != 200 || !strings.Contains(signup.Body.String(), "turnstile/v0/api.js") {
		t.Fatal("hosted signup or Turnstile changed", signup.Code, signup.Body)
	}
	assertHTMLPolicy(t, signup.Header(), htmlpolicy.Signup)
	if e = db.SetSetting(ctx, fmt.Sprintf("brand.%d.thankyou_html", brand.ID), "<h1>Custom thanks</h1><script>alert(1)</script>"); e != nil {
		t.Fatal(e)
	}
	// Settings must round-trip the optional welcome configuration through authenticated APIs.
	settingsPath := fmt.Sprintf("/api/v1/settings?brand_id=%d", brand.ID)
	w := request(m, "PATCH", settingsPath, `{"thankyou_email":{"subject":"Welcome","html":"<p>Welcome</p>"}}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("save welcome: %d %s", w.Code, w.Body.String())
	}
	w = request(m, "GET", settingsPath, "", &cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "thankyou_email") {
		t.Fatalf("read welcome: %d %s", w.Code, w.Body.String())
	}
	for _, accept := range []string{"text/html", "application/json"} {
		form := url.Values{"email": {strings.ReplaceAll(accept, "/", "-") + "@example.org"}, "consent": {"on"}}
		r := httptest.NewRequest(http.MethodPost, "/subscribe/"+list.UUID, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", accept)
		w = httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("subscribe %s: %d %s", accept, w.Code, w.Body.String())
		}
		if accept == "text/html" {
			assertHTMLPolicy(t, w.Header(), htmlpolicy.Custom)
			if !strings.Contains(w.Body.String(), "Custom thanks") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("hosted thankyou not sandboxed")
			}
		} else if !strings.Contains(w.Body.String(), "confirmation_pending") || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal("API response changed")
		}
	}
	v, e := service.Upsert(ctx, subscribers.UpsertInput{BrandID: brand.ID, Email: "confirm@example.org", ListIDs: []int64{list.ID}})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest(http.MethodGet, "/confirm/"+v.ConfirmationTokens[list.ID], nil)
	r.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Custom thanks") {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	assertHTMLPolicy(t, w.Header(), htmlpolicy.Custom)
}
