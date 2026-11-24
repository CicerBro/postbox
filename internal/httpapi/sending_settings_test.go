package httpapi

import "testing"

func TestSendingSettingsRequireAdminAndPersistGmailCap(t *testing.T) {
	_, mux, db, admin, csrf := setup(t)
	const path = "/api/v1/sending-settings"
	w := request(mux, "GET", path, "", &admin, csrf)
	if w.Code != 200 || w.Body.String() != "{\"gmail_sends_per_minute\":0}\n" {
		t.Fatalf("default: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"gmail_sends_per_minute":null}`, `{"gmail_sends_per_minute":2.5}`, `{"gmail_sends_per_minute":-1}`, `{"gmail_sends_per_minute":60001}`, `{"gmail_sends_per_minute":"1"}`, `{"gmail_sends_per_minute":1,"enabled":true}`} {
		w = request(mux, "PATCH", path, body, &admin, csrf)
		if w.Code != 400 {
			t.Fatalf("invalid %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w = request(mux, "PATCH", path, `{"gmail_sends_per_minute":1}`, &admin, csrf)
	if w.Code != 200 || w.Body.String() != "{\"gmail_sends_per_minute\":1}\n" {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	w = request(mux, "GET", path, "", &admin, csrf)
	if w.Code != 200 || w.Body.String() != "{\"gmail_sends_per_minute\":1}\n" {
		t.Fatalf("persisted: %d %s", w.Code, w.Body.String())
	}
	w = request(mux, "PATCH", path, `{"gmail_sends_per_minute":2}`, &admin, "")
	if w.Code != 403 {
		t.Fatalf("missing CSRF: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Writer.Exec(`UPDATE users SET role='brand_user' WHERE email='admin@example.org'`); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PATCH"} {
		w = request(mux, method, path, `{"gmail_sends_per_minute":2}`, &admin, csrf)
		if w.Code != 403 {
			t.Fatalf("member %s: %d %s", method, w.Code, w.Body.String())
		}
	}
}
