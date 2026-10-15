package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaglineSettings(t *testing.T) {
	a, mux, db, admin, csrf := setup(t)
	const path = "/api/v1/tagline-settings"
	read := func(w *httptest.ResponseRecorder, public bool) TaglineSettings {
		t.Helper()
		if w.Code != http.StatusOK {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}
		var settings TaglineSettings
		var err error
		if public {
			var config struct {
				Tagline TaglineSettings `json:"tagline"`
			}
			err = json.Unmarshal(w.Body.Bytes(), &config)
			settings = config.Tagline
		} else {
			err = json.Unmarshal(w.Body.Bytes(), &settings)
		}
		if err != nil {
			t.Fatal(err)
		}
		return settings
	}
	check := func(want TaglineSettings) {
		t.Helper()
		if got := read(request(mux, "GET", path, "", &admin, csrf), false); got != want {
			t.Fatalf("admin settings: got %+v want %+v", got, want)
		}
		w := request(mux, "GET", "/api/v1/auth/config", "", nil, "")
		if got := read(w, true); got != want {
			t.Fatalf("public settings: got %+v want %+v", got, want)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("public config may be cached")
		}
	}
	check(TaglineSettings{true, defaultTagline})
	custom := "Our newsletter workspace. <b>Plain text</b> **only**.\nWelkom 👋"
	for _, enabled := range []bool{true, false, true} {
		want := TaglineSettings{enabled, custom}
		body, _ := json.Marshal(want)
		if w := request(mux, "PATCH", path, string(body), &admin, csrf); w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		check(want)
		// Verify the setting was persisted, independently of the API instance.
		stored, err := db.Setting(context.Background(), taglineSettingsKey)
		if err != nil {
			t.Fatal(err)
		}
		var got TaglineSettings
		if json.Unmarshal([]byte(stored), &got) != nil || got != want {
			t.Fatal("setting not persisted", stored)
		}
	}
	for _, body := range []string{
		`{}`, `null`, `{"enabled":true}`, `{"enabled":null,"text":"x"}`,
		`{"enabled":true,"text":null}`, `{"enabled":"true","text":"x"}`,
		`{"enabled":true,"text":"  "}`, `{"enabled":true,"text":"x","html":"x"}`,
		`{"enabled":true,"text":"` + strings.Repeat("x", 501) + `"}`,
	} {
		if w := request(mux, "PATCH", path, body, &admin, csrf); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid input accepted: %s: %d", body, w.Code)
		}
	}
	check(TaglineSettings{true, custom})
	if w := request(mux, "GET", path, "", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous admin read allowed", w.Code)
	}
	if w := request(mux, "PATCH", path, `{"enabled":false,"text":"x"}`, &admin, ""); w.Code != http.StatusForbidden {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	for _, caller := range []*identity{{UserID: 2}, {BrandID: 1, Admin: true}} {
		for _, method := range []string{"GET", "PATCH"} {
			r := httptest.NewRequest(method, path, strings.NewReader(`{"enabled":false,"text":"x"}`))
			r = r.WithContext(context.WithValue(r.Context(), identityKey{}, caller))
			w := httptest.NewRecorder()
			a.taglineSettings(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatal("non-admin session allowed", w.Code)
			}
		}
	}
}
