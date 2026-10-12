package httpapi

import (
	"context"
	"fmt"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestMessageSettingsClearAndScope(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	service := subscribers.New(db)
	brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Messages", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/settings?brand_id=%d", brand.ID)
	for _, id := range []int64{brand.ID, other.ID} {
		w := request(mux, "PATCH", fmt.Sprintf("/api/v1/settings?brand_id=%d", id), `{"thankyou_html":"Custom thanks","thankyou_email":{"subject":"Welcome","html":"<p>Welcome</p>"},"confirmation_email":{"subject":"Confirm","html":"Confirm"}}`, &cookie, csrf)
		if w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body)
		}
	}
	// Reject unknown keys atomically, even if the same request clears a setting.
	w := request(mux, "PATCH", path, `{"thankyou_html":null,"unknown":null}`, &cookie, csrf)
	if w.Code == 200 {
		t.Fatal("unknown setting accepted")
	}
	w = request(mux, "GET", path, "", &cookie, csrf)
	if !strings.Contains(w.Body.String(), "Custom thanks") {
		t.Fatal("failed request cleared setting")
	}
	w = request(mux, "PATCH", path, `{"thankyou_html":null,"thankyou_email":null}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	w = request(mux, "GET", path, "", &cookie, csrf)
	if strings.Contains(w.Body.String(), "thankyou") || !strings.Contains(w.Body.String(), "confirmation_email") {
		t.Fatalf("clear/omitted: %s", w.Body)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/settings?brand_id=%d", other.ID), "", &cookie, csrf)
	if !strings.Contains(w.Body.String(), "thankyou_email") {
		t.Fatalf("other brand modified: %s", w.Body)
	}
	w = request(mux, "PATCH", path, `{"confirmation_email":null}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("clear all: %d %s", w.Code, w.Body)
	}
	w = request(mux, "GET", path, "", &cookie, csrf)
	if strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("expected no overrides: %s", w.Body)
	}
}
