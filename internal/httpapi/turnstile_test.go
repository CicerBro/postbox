package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/pquerna/otp/totp"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type turnstileTransport func(*http.Request) (*http.Response, error)

func (f turnstileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthTurnstileRejectsBeforeCredentials(t *testing.T) {
	a, mux, db, _, _ := setup(t)
	a.cfg.BaseURL = "https://example.org"
	a.cfg.TurnstileSiteKey = "public-key"
	a.cfg.TurnstileSecret = "private-key"
	var before int
	_ = db.Reader.QueryRow("SELECT count(*) FROM sessions").Scan(&before)
	for _, tc := range []struct {
		name, token, response string
		status                int
		transportError        bool
		calls                 int
	}{
		{"missing", "", `{"success":true}`, 200, false, 0},
		{"oversized", strings.Repeat("a", 2049), `{"success":true}`, 200, false, 0},
		{"invalid or expired", "invalid", `{"success":false}`, 200, false, 1},
		{"wrong action", "token", `{"success":true,"hostname":"example.org","action":"subscribe"}`, 200, false, 1},
		{"wrong hostname", "token", `{"success":true,"hostname":"attacker.org","action":"login"}`, 200, false, 1},
		{"missing metadata", "token", `{"success":true}`, 200, false, 1},
		{"malformed", "token", `not JSON`, 200, false, 1},
		{"upstream error", "token", `{"success":true,"hostname":"example.org","action":"login"}`, 500, false, 1},
		{"network failure", "token", "", 200, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.limits = map[string]*bucket{}
			calls := 0
			a.turnstileClient = &http.Client{Transport: turnstileTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.transportError {
					return nil, errors.New("upstream failure containing private-key")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.response)), Header: http.Header{}}, nil
			})}
			body, _ := json.Marshal(map[string]string{"email": "admin@example.org", "password": "long-test-password", "turnstile_token": tc.token})
			w := request(mux, "POST", "/api/v1/auth/login", string(body), nil, "")
			if w.Code != 403 || len(w.Result().Cookies()) != 0 || calls != tc.calls || strings.Contains(w.Body.String(), "private-key") {
				t.Fatalf("challenge bypass or leaked error: %d %s calls=%d", w.Code, w.Body.String(), calls)
			}
			var after int
			_ = db.Reader.QueryRow("SELECT count(*) FROM sessions").Scan(&after)
			if after != before {
				t.Fatal("failed challenge created a session")
			}
		})
	}
}

func TestAuthTurnstileTOTPAndReplay(t *testing.T) {
	a, mux, db, _, _ := setup(t)
	a.cfg.BaseURL = "https://example.org"
	a.cfg.TurnstileSiteKey, a.cfg.TurnstileSecret = "public-key", "private-key"
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Postbox", AccountName: "admin@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec("UPDATE users SET totp_secret=?", key.Secret()); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	a.turnstileClient = &http.Client{Transport: turnstileTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://challenges.cloudflare.com/turnstile/v0/siteverify" || r.Method != "POST" {
			t.Fatal("wrong verification endpoint")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("secret") != "private-key" || r.FormValue("remoteip") == "" {
			t.Fatal("missing secret or client IP")
		}
		token := r.FormValue("response")
		success := !used[token]
		used[token] = true
		body, _ := json.Marshal(map[string]any{"success": success, "hostname": "example.org", "action": "login"})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})}
	login := func(token, code string) int {
		body, _ := json.Marshal(map[string]string{"email": "admin@example.org", "password": "long-test-password", "totp": code, "turnstile_token": token})
		return request(mux, "POST", "/api/v1/auth/login", string(body), nil, "").Code
	}
	if status := login("first", ""); status != 202 {
		t.Fatal("TOTP prompt missing", status)
	}
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status := login("first", code); status != 403 {
		t.Fatal("replayed token accepted", status)
	}
	if status := login("second", "bad"); status != 401 {
		t.Fatal("invalid TOTP accepted", status)
	}
	if status := login("third", code); status != 200 {
		t.Fatal("fresh token and TOTP rejected", status)
	}
}

func TestAuthTurnstileConfigAndPasskeyBegin(t *testing.T) {
	a, mux, _, _, _ := setup(t)
	a.passkeys, _ = passkeyRP("https://example.org")
	a.cfg.BaseURL = "https://example.org"
	a.cfg.TurnstileSiteKey, a.cfg.TurnstileSecret = "public-key", "private-key"
	w := request(mux, "GET", "/api/v1/auth/config", "", nil, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("unsafe auth config", w.Body.String())
	}
	var config map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil || config["turnstile_required"] != true || config["turnstile_site_key"] != "public-key" || len(config) != 2 {
		t.Fatal("invalid auth config", config)
	}
	w = request(mux, "POST", "/api/v1/auth/passkeys/login/begin", `{}`, nil, "")
	if w.Code != 403 || len(w.Result().Cookies()) != 0 {
		t.Fatal("passkey challenge bypass", w.Code)
	}
	a.turnstileClient = &http.Client{Transport: turnstileTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"hostname":"example.org","action":"login"}`)), Header: http.Header{}}, nil
	})}
	w = request(mux, "POST", "/api/v1/auth/passkeys/login/begin", `{"turnstile_token":"fresh"}`, nil, "")
	if w.Code != 200 || len(w.Result().Cookies()) == 0 {
		t.Fatal("verified passkey begin rejected", w.Code, w.Body.String())
	}
	for _, keys := range [][2]string{{"public-key", ""}, {"", "private-key"}} {
		a.cfg.TurnstileSiteKey, a.cfg.TurnstileSecret = keys[0], keys[1]
		w = request(mux, "POST", "/api/v1/auth/login", `{"email":"admin@example.org","password":"long-test-password"}`, nil, "")
		if w.Code != 503 {
			t.Fatal("partial config failed open", w.Code)
		}
	}
}
