package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"postbox/internal/htmlpolicy"
	"postbox/internal/subscribers"
)

func TestHostedSubscriptionPagePreservesFormContract(t *testing.T) {
	a, mux, db, _, _ := setup(t)
	service := subscribers.New(db)
	brand, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: `Brand <script>alert(1)</script>`, FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.CreateList(context.Background(), subscribers.List{BrandID: brand.ID, Name: `Readers <script>alert(2)</script>`, OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/subscribe/" + list.UUID
	for _, siteKey := range []string{"", "public-turnstile-key"} {
		a.cfg.TurnstileSiteKey = siteKey
		w := request(mux, "GET", path, "", nil, "")
		if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != string(htmlpolicy.Signup) || w.Header().Get("Cache-Control") != "no-store, no-transform" {
			t.Fatalf("signup response: %d %v", w.Code, w.Header())
		}
		body := w.Body.String()
		for _, part := range []string{`name="viewport"`, `class="brand"`, `class="card"`, `Brand &lt;script&gt;`, `Readers &lt;script&gt;`, `method="post"`, `type="email" name="email"`, `name="name"`, `name="website" tabindex="-1" autocomplete="off"`, `type="checkbox" name="consent" required`, `class="primary" type="submit"`} {
			if !strings.Contains(body, part) {
				t.Fatalf("missing signup contract %q", part)
			}
		}
		if strings.Contains(body, "<script>alert(") {
			t.Fatal("signup page included unescaped brand/list HTML")
		}
		hasTurnstile := strings.Contains(body, `src="https://challenges.cloudflare.com/turnstile/v0/api.js"`) && strings.Contains(body, `data-sitekey="public-turnstile-key"`)
		if hasTurnstile != (siteKey != "") {
			t.Fatalf("Turnstile mismatch for site key %q", siteKey)
		}
	}
	post := func(values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := post(url.Values{"email": {"reader@example.org"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("missing consent accepted: %d %s", w.Code, w.Body.String())
	}
	w := post(url.Values{"email": {"reader@example.org"}, "name": {"Reader"}, "consent": {"on"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `class="card"`) || !strings.Contains(w.Body.String(), "Thank you for subscribing.") || strings.Contains(w.Body.String(), "<script>alert(") {
		t.Fatalf("hosted completion: %d %s", w.Code, w.Body.String())
	}
	if err = db.SetSetting(context.Background(), fmt.Sprintf("brand.%d.thankyou_html", brand.ID), "<h1>Custom thank you</h1>"); err != nil {
		t.Fatal(err)
	}
	w = post(url.Values{"email": {"custom@example.org"}, "consent": {"on"}})
	if w.Code != http.StatusOK || w.Body.String() != "<h1>Custom thank you</h1>" {
		t.Fatalf("custom completion changed: %d %s", w.Code, w.Body.String())
	}
}
