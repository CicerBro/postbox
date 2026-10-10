package tracking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"postbox/internal/subscribers"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
)

func TestPublicAllOptOutAndExplicitDoubleRenewal(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	base := "https://track.example.com"
	service := New(db, &Config{TokenSecret: secret, BaseURL: base})
	mux := http.NewServeMux()
	service.Register(mux)
	tok := token.Encode(secret, 1, 1)
	request := func(path, body string) {
		t.Helper()
		r := httptest.NewRequest("POST", base+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status %d %s", w.Code, w.Body.String())
		}
	}
	request("/u/"+tok, "list=all")
	var at *int64
	if err := db.Reader.QueryRow(`SELECT marketing_unsubscribed_at FROM subscribers WHERE id=1`).Scan(&at); err != nil || at == nil {
		t.Fatalf("global preference missing %v %v", at, err)
	}
	if _, err := db.Writer.Exec(`UPDATE lists SET opt_in='double' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	request("/u/"+tok+"/resubscribe", "list=1")
	if err := db.Reader.QueryRow(`SELECT marketing_unsubscribed_at FROM subscribers WHERE id=1`).Scan(&at); err != nil || at == nil {
		t.Fatal("double opt-in cleared before confirmation", err)
	}
	var payload string
	if err := db.Reader.QueryRow(`SELECT payload FROM jobs WHERE queue='transactional_email' AND json_extract(payload,'$.confirmation_token') IS NOT NULL ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Token string `json:"confirmation_token"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if err := subscribers.New(db).Confirm(context.Background(), p.Token); err != nil {
		t.Fatal(err)
	}
	if err := db.Reader.QueryRow(`SELECT marketing_unsubscribed_at FROM subscribers WHERE id=1`).Scan(&at); err != nil || at != nil {
		t.Fatal("confirmed renewal did not clear preference", err)
	}
}
