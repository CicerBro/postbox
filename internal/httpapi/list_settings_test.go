package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestListSettingsScopeClearAndConsent(t *testing.T) {
	a, m, db, cookie, csrf := setup(t)
	a.cfg.BaseURL = "https://example.org"
	ctx := context.Background()
	service := subscribers.New(db)
	brand, e := service.CreateBrand(ctx, subscribers.Brand{Name: "Messages", FromEmail: "sender@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	other, e := service.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	list, e := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers", OptIn: "double"})
	if e != nil {
		t.Fatal(e)
	}
	path := fmt.Sprintf("/api/v1/lists/%d/settings?brand_id=%d", list.ID, brand.ID)
	if e = db.SetBrandSetting(ctx, brand.ID, "confirmation_email", `{"subject":"Brand confirmation","html":"<a href=\"{{ confirmation_url }}\">Confirm</a>"}`); e != nil {
		t.Fatal(e)
	}
	if e = db.SetBrandSetting(ctx, brand.ID, "thankyou_html", "Brand thanks"); e != nil {
		t.Fatal(e)
	}
	bad := request(m, "PATCH", path, `{"thankyou_html":"Must not persist","opt_in":"single"}`, &cookie, csrf)
	if bad.Code != 400 {
		t.Fatalf("unknown allowed: %d %s", bad.Code, bad.Body)
	}
	w := request(m, "PATCH", fmt.Sprintf("/api/v1/lists/%d/settings?brand_id=%d", list.ID, other.ID), `{"thankyou_html":"Other"}`, &cookie, csrf)
	if w.Code != 404 {
		t.Fatalf("cross-brand write: %d", w.Code)
	}
	w = request(m, "PATCH", path, `{"confirmation_email":{"subject":"List confirmation","html":"<a href=\"{{ confirmation_url }}\">List confirm</a>"},"confirmation_page_html":"Pending list confirmation","thankyou_email":{"subject":"List welcome","html":"Welcome"},"thankyou_html":"List thanks"}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	form := url.Values{"email": {"reader@example.org"}, "consent": {"on"}}
	r := httptest.NewRequest("POST", "/subscribe/"+list.UUID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Pending list confirmation") {
		t.Fatalf("pending page: %d %s", w.Code, w.Body)
	}
	var payload string
	var welcomeCount int
	if e = db.Reader.QueryRow("SELECT payload FROM jobs WHERE queue='transactional_email'").Scan(&payload); e != nil || !strings.Contains(payload, "List confirmation") {
		t.Fatalf("confirmation: %v %s", e, payload)
	}
	if e = db.Reader.QueryRow("SELECT count(*) FROM jobs WHERE json_extract(payload,'$.purpose')='thankyou'").Scan(&welcomeCount); e != nil || welcomeCount != 0 {
		t.Fatalf("welcome before consent: %d %v", welcomeCount, e)
	}
	var mail struct {
		Token string `json:"confirmation_token"`
	}
	if e = json.Unmarshal([]byte(payload), &mail); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest(http.MethodGet, "/confirm/"+mail.Token, nil)
	r.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "List thanks" {
		t.Fatalf("confirmed page: %d %s", w.Code, w.Body)
	}
	if e = db.Reader.QueryRow("SELECT payload FROM jobs WHERE json_extract(payload,'$.purpose')='thankyou'").Scan(&payload); e != nil || !strings.Contains(payload, "List welcome") {
		t.Fatalf("welcome override: %v %s", e, payload)
	}
	w = request(m, "PATCH", path, `{"confirmation_email":null,"thankyou_html":""}`, &cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Brand confirmation") || !strings.Contains(w.Body.String(), "Brand thanks") {
		t.Fatalf("clear inheritance: %d %s", w.Code, w.Body)
	}
	var out struct{ Overrides map[string]any }
	json.Unmarshal(w.Body.Bytes(), &out)
	if _, ok := out.Overrides["thankyou_html"]; ok {
		t.Fatal("cleared override retained")
	}
	var opt string
	if e = db.Reader.QueryRow("SELECT opt_in FROM lists WHERE id=?", list.ID).Scan(&opt); e != nil || opt != "double" {
		t.Fatalf("opt-in changed: %s %v", opt, e)
	}
}
