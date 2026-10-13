package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTimezonePreferencesPersistenceAndValidation(t *testing.T) {
	a, mux, db, cookie, csrf := setup(t)
	read := func(m http.Handler, want string) {
		t.Helper()
		w := request(m, "GET", "/api/v1/auth/session", "", &cookie, "")
		var session SessionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || w.Code != 200 || session.Timezone != want {
			t.Fatalf("session timezone: %d %s (%v)", w.Code, w.Body.String(), err)
		}
	}
	read(mux, "")
	for _, zone := range []string{"Europe/London", "America/New_York", "Asia/Kathmandu", "UTC", ""} {
		body, _ := json.Marshal(PreferencesResponse{Timezone: zone})
		w := request(mux, "PATCH", "/api/v1/auth/preferences", string(body), &cookie, csrf)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("save %q: %d %s", zone, w.Code, w.Body.String())
		}
		var saved PreferencesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Timezone != zone {
			t.Fatalf("saved preference: %s %v", w.Body.String(), err)
		}
		read(mux, zone)
		restarted := New(db, a.engine, a.cfg)
		restartMux := http.NewServeMux()
		restarted.Register(restartMux)
		read(restartMux, zone)
	}
	request(mux, "PATCH", "/api/v1/auth/preferences", `{"timezone":"Europe/London"}`, &cookie, csrf)
	for _, body := range []string{`{}`, `{"timezone":null}`, `{"timezone":"Local"}`, `{"timezone":"Mars/Olympus"}`, `{"timezone":"../UTC"}`, `{"timezone":" UTC "}`, `{"timezone":42}`, `{"timezone":"UTC","user_id":2}`} {
		w := request(mux, "PATCH", "/api/v1/auth/preferences", body, &cookie, csrf)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid preference %s: %d %s", body, w.Code, w.Body.String())
		}
		read(mux, "Europe/London")
	}
}

func TestTimezonePreferencesSessionAndUserIsolation(t *testing.T) {
	_, mux, db, admin, adminCSRF := setup(t)
	for _, tc := range []struct {
		cookie *http.Cookie
		csrf   string
		status int
	}{{nil, "", 401}, {&admin, "", 403}, {&admin, "invalid", 403}} {
		if w := request(mux, "PATCH", "/api/v1/auth/preferences", `{"timezone":"UTC"}`, tc.cookie, tc.csrf); w.Code != tc.status {
			t.Fatalf("authentication boundary: %d %s", w.Code, w.Body.String())
		}
	}
	w := request(mux, "POST", "/api/v1/users", `{"email":"viewer@example.org","password":"viewer-long-password"}`, &admin, adminCSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "POST", "/api/v1/auth/login", `{"email":"viewer@example.org","password":"viewer-long-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	viewer := w.Result().Cookies()[0]
	var login LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	w = request(mux, "PATCH", "/api/v1/auth/preferences", `{"timezone":"Asia/Tokyo"}`, viewer, login.CSRFToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var zone string
	if err := db.Reader.QueryRow("SELECT timezone FROM users WHERE email='admin@example.org'").Scan(&zone); err != nil || zone != "" {
		t.Fatalf("another user's timezone changed: %q %v", zone, err)
	}
	if err := db.Reader.QueryRow("SELECT timezone FROM users WHERE email='viewer@example.org'").Scan(&zone); err != nil || zone != "Asia/Tokyo" {
		t.Fatalf("non-admin timezone was not saved: %q %v", zone, err)
	}
	w = request(mux, "POST", "/api/v1/brands", `{"name":"Brand","from_email":"from@example.org"}`, &admin, adminCSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"integration","scopes":["read","write"]}`, &admin, adminCSRF)
	var key struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil || key.Secret == "" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	r := httptest.NewRequest("PATCH", "/api/v1/auth/preferences", strings.NewReader(`{"timezone":"UTC"}`))
	r.Header.Set("Authorization", "Bearer "+key.Secret)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	if out.Code != http.StatusForbidden {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestDisplayNamePreferences(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	read := func(want string) {
		t.Helper()
		w := request(mux, "GET", "/api/v1/auth/session", "", &cookie, "")
		var session SessionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || w.Code != 200 || session.DisplayName != want || session.Email != "admin@example.org" || !session.Admin {
			t.Fatalf("session identity: %d %s (%v)", w.Code, w.Body.String(), err)
		}
	}
	read("")
	for _, name := range []string{"  Ada Lovelace  ", "李明", ""} {
		body, _ := json.Marshal(map[string]string{"display_name": name})
		w := request(mux, "PATCH", "/api/v1/auth/preferences", string(body), &cookie, csrf)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		read(strings.TrimSpace(name))
	}
	request(mux, "PATCH", "/api/v1/auth/preferences", `{"timezone":"Europe/London","display_name":"Ada"}`, &cookie, csrf)
	request(mux, "PATCH", "/api/v1/auth/preferences", `{"timezone":"UTC"}`, &cookie, csrf)
	read("Ada")
	for _, body := range []string{`{"display_name":null}`, `{"display_name":42}`, `{"display_name":"Ada","user_id":2}`, `{"display_name":"` + strings.Repeat("界", 101) + `"}`} {
		if w := request(mux, "PATCH", "/api/v1/auth/preferences", body, &cookie, csrf); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		read("Ada")
	}
	if w := request(mux, "PATCH", "/api/v1/auth/preferences", `{"display_name":"Other"}`, &cookie, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(mux, "POST", "/api/v1/users", `{"email":"viewer@example.org","password":"viewer-long-password","display_name":" Viewer "}`, &cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request(mux, "POST", "/api/v1/auth/login", `{"email":"viewer@example.org","password":"viewer-long-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	viewer := w.Result().Cookies()[0]
	var login LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if w = request(mux, "PATCH", "/api/v1/auth/preferences", `{"display_name":"Member"}`, viewer, login.CSRFToken); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	read("Ada")
	var name, zone string
	if err := db.Reader.QueryRow("SELECT display_name,timezone FROM users WHERE email='viewer@example.org'").Scan(&name, &zone); err != nil || name != "Member" || zone != "" {
		t.Fatalf("member preferences: %q %q %v", name, zone, err)
	}
	if w = request(mux, "PATCH", "/api/v1/users/2", `{"display_name":""}`, &cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := db.Reader.QueryRow("SELECT display_name FROM users WHERE id=2").Scan(&name); err != nil || name != "" {
		t.Fatalf("admin clear: %q %v", name, err)
	}
}
