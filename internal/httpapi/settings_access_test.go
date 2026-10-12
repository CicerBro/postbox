package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func settingsMember(t *testing.T, m http.Handler, admin *http.Cookie, csrf string) (int64, *http.Cookie) {
	t.Helper()
	w := request(m, "POST", "/api/v1/users", `{"email":"settings@example.org","password":"settings-long-password"}`, admin, csrf)
	var user struct{ ID int64 }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &user) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(m, "POST", "/api/v1/auth/login", `{"email":"settings@example.org","password":"settings-long-password"}`, nil, "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	return user.ID, w.Result().Cookies()[0]
}

func settingsBearer(t *testing.T, m http.Handler, admin *http.Cookie, csrf string) string {
	t.Helper()
	w := request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"settings test","scopes":["read"]}`, admin, csrf)
	var key struct{ Secret string }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &key) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return key.Secret
}

func TestSettingsBrandPermissionsMatchCaller(t *testing.T) {
	_, m, _, admin, csrf := setup(t)
	for _, name := range []string{"one", "two", "private"} {
		w := request(m, "POST", "/api/v1/brands", fmt.Sprintf(`{"name":%q,"from_email":"from@example.org"}`, name), &admin, csrf)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	id, member := settingsMember(t, m, &admin, csrf)
	for brand, permissions := range map[int]string{1: `["read"]`, 2: `["write","send"]`} {
		w := request(m, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/%d", id, brand), `{"permissions":`+permissions+`}`, &admin, csrf)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	type brandResponse struct {
		ID          int64    `json:"id"`
		Permissions []string `json:"permissions"`
	}
	check := func(w *httptest.ResponseRecorder, expected []brandResponse) {
		t.Helper()
		var brands []brandResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &brands) != nil || !reflect.DeepEqual(brands, expected) {
			t.Fatalf("caller grants: got %d %s, want %+v", w.Code, w.Body.String(), expected)
		}
	}
	check(request(m, "GET", "/api/v1/brands", "", member, ""), []brandResponse{{1, []string{"read"}}, {2, []string{"write", "send"}}})
	check(request(m, "GET", "/api/v1/brands/1", "", member, ""), []brandResponse{{1, []string{"read"}}})
	check(request(m, "GET", "/api/v1/brands", "", &admin, ""), []brandResponse{{1, []string{"read", "write", "send"}}, {2, []string{"read", "write", "send"}}, {3, []string{"read", "write", "send"}}})
	if w := request(m, "GET", "/api/v1/brands/3", "", member, ""); w.Code != http.StatusForbidden {
		t.Fatal("other brand accessible", w.Code, w.Body.String())
	}
	bearer := settingsBearer(t, m, &admin, csrf)
	r := httptest.NewRequest("GET", "/api/v1/brands", nil)
	r.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	check(w, []brandResponse{{1, []string{"read"}}})
	w = request(m, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/1", id), `{"permissions":["read","write"]}`, &admin, csrf)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	check(request(m, "GET", "/api/v1/brands/1", "", member, ""), []brandResponse{{1, []string{"read", "write"}}})
}

func TestDeploymentSettingsRequiresAdminSessionAndOmitsSecrets(t *testing.T) {
	a, m, _, admin, csrf := setup(t)
	a.cfg.BaseURL = "https://postbox.example.org"
	a.cfg.SecureCookies = true
	a.cfg.SessionTTL = 2 * time.Hour
	a.cfg.TurnstileSecret = "private-turnstile-secret"
	a.cfg.TurnstileSiteKey = "public-turnstile-site-key"
	a.cfg.BackupsConfigured = true
	a.cfg.RawEventRetentionDays = 90
	a.cfg.MaxSendWorkers = 512
	a.cfg.RecoveryPolicy = "unknown"
	a.cfg.SoftBounceLimit = 3
	w := request(m, "GET", "/api/v1/deployment-settings", "", &admin, "")
	var data map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(data) != 11 || data["base_url"] != a.cfg.BaseURL || data["secure_cookies"] != true || data["session_ttl_seconds"] != float64(7200) || data["turnstile_configured"] != true || data["backups_configured"] != true || data["raw_event_retention_days"] != float64(90) {
		t.Fatal("unexpected deployment settings", w.Body.String())
	}
	if strings.Contains(w.Body.String(), a.cfg.TurnstileSecret) || strings.Contains(w.Body.String(), a.cfg.TurnstileSiteKey) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("deployment secret or caching leak", w.Body.String())
	}
	if w = request(m, "GET", "/api/v1/deployment-settings", "", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous deployment access", w.Code)
	}
	_, member := settingsMember(t, m, &admin, csrf)
	if w = request(m, "GET", "/api/v1/deployment-settings", "", member, ""); w.Code != http.StatusForbidden {
		t.Fatal("member deployment access", w.Code)
	}
	w = request(m, "POST", "/api/v1/brands", `{"name":"one","from_email":"from@example.org"}`, &admin, csrf)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	bearer := settingsBearer(t, m, &admin, csrf)
	r := httptest.NewRequest("GET", "/api/v1/deployment-settings", nil)
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.AddCookie(&admin)
	w = httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("bearer deployment access", w.Code, w.Body.String())
	}
}
