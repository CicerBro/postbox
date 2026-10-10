package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func readSessionCSRF(t *testing.T, mux http.Handler, cookie *http.Cookie) string {
	t.Helper()
	w := request(mux, "GET", "/api/v1/auth/session", "", cookie, "")
	if w.Code != http.StatusOK {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session response may be cached")
	}
	var data struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.CSRF == "" {
		t.Fatalf("session token: %q %v", data.CSRF, err)
	}
	return data.CSRF
}

func TestSessionCSRFStableAcrossTabsAndReloads(t *testing.T) {
	a, mux, db, cookie, loginCSRF := setup(t)
	// Tabs share the HttpOnly cookie but retain independent in-memory CSRF tokens.
	tabA := readSessionCSRF(t, mux, &cookie)
	tabB := readSessionCSRF(t, mux, &cookie)
	if tabA != loginCSRF || tabB != tabA {
		t.Fatal("opening a second tab rotated the first tab's CSRF token")
	}
	var before []byte
	if err := db.Reader.QueryRow("SELECT csrf_hash FROM sessions WHERE token_hash=?", digest(cookie.Value)).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Concurrent reads and reloads must not write or rotate session state.
	var wg sync.WaitGroup
	errors := make(chan string, 16)
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(mux, "GET", "/api/v1/auth/session", "", &cookie, "")
			var data struct {
				CSRF string `json:"csrf_token"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 || data.CSRF != tabA {
				errors <- fmt.Sprintf("%d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var after []byte
	if err := db.Reader.QueryRow("SELECT csrf_hash FROM sessions WHERE token_hash=?", digest(cookie.Value)).Scan(&after); err != nil || !equal(before, after) {
		t.Fatalf("session read changed persisted CSRF hash: %v", err)
	}
	// A new API instance models a process restart with no in-memory token state.
	restarted := New(db, a.engine, a.cfg)
	restartMux := http.NewServeMux()
	restarted.Register(restartMux)
	if readSessionCSRF(t, restartMux, &cookie) != tabA {
		t.Fatal("restart changed CSRF token")
	}
	for n, token := range []string{tabA, tabB, readSessionCSRF(t, mux, &cookie)} {
		w := request(mux, "POST", "/api/v1/brands", fmt.Sprintf(`{"name":"Tab %d","from_email":"from@example.org"}`, n), &cookie, token)
		if w.Code != 200 {
			t.Fatalf("tab write: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestSessionCSRFAuthenticationBoundaries(t *testing.T) {
	a, mux, db, first, firstCSRF := setup(t)
	a.cfg.SecureCookies = true
	w := request(mux, "POST", "/api/v1/auth/login", `{"email":"admin@example.org","password":"long-test-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	second := w.Result().Cookies()[0]
	if !second.Secure || !second.HttpOnly || second.SameSite != http.SameSiteStrictMode {
		t.Fatal("login cookie protection")
	}
	secondCSRF := readSessionCSRF(t, mux, second)
	if first.Value == second.Value || firstCSRF == secondCSRF {
		t.Fatal("new login reused session credentials")
	}
	body := `{"name":"Protected","from_email":"from@example.org"}`
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		token  string
	}{
		{"missing", &first, ""}, {"invalid", &first, "invalid"},
		{"other session", &first, secondCSRF}, {"old token with new session", second, firstCSRF},
		{"session cookie is not CSRF", &first, first.Value},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := request(mux, "POST", "/api/v1/brands", body, tc.cookie, tc.token); w.Code != 403 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	// Logout in one tab revokes the shared session for all tabs immediately.
	w = request(mux, "POST", "/api/v1/auth/logout", "", &first, firstCSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cleared := w.Result().Cookies()[0]
	if cleared.MaxAge != -1 || !cleared.HttpOnly || cleared.SameSite != http.SameSiteStrictMode {
		t.Fatal("logout cookie protection")
	}
	for _, method := range []string{"GET", "POST"} {
		path := "/api/v1/auth/session"
		if method == "POST" {
			path = "/api/v1/brands"
		}
		if w := request(mux, method, path, body, &first, firstCSRF); w.Code != 401 {
			t.Fatalf("logged out tab: %d", w.Code)
		}
	}
	if w := request(mux, "POST", "/api/v1/brands", body, second, secondCSRF); w.Code != 200 {
		t.Fatal("independent session revoked", w.Code)
	}
	if _, err := db.Writer.Exec("UPDATE sessions SET expires_at=? WHERE token_hash=?", time.Now().Add(-time.Minute).UnixMilli(), digest(second.Value)); err != nil {
		t.Fatal(err)
	}
	if w := request(mux, "POST", "/api/v1/brands", body, second, secondCSRF); w.Code != 401 {
		t.Fatal("expired session authorized", w.Code)
	}
	if _, err := db.Writer.Exec("UPDATE sessions SET expires_at=? WHERE token_hash=?", time.Now().Add(time.Hour).UnixMilli(), digest(second.Value)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec("DELETE FROM sessions WHERE token_hash=?", digest(second.Value)); err != nil {
		t.Fatal(err)
	}
	if w := request(mux, "POST", "/api/v1/brands", body, second, secondCSRF); w.Code != 401 {
		t.Fatal("revoked session authorized", w.Code)
	}
}
