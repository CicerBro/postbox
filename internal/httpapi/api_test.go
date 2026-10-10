package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"postbox/internal/contacts"
	"postbox/internal/send"
	"postbox/internal/store"
	"strings"
	"testing"
	"time"
)

type testSender struct{}

func (testSender) GetAccount(context.Context) (send.Quota, error) {
	return send.Quota{MaxSendRate: 100, Max24HourSend: 1000}, nil
}
func (testSender) Send(context.Context, send.Request) (string, error) { return "message-test", nil }
func setup(t *testing.T) (*API, *http.ServeMux, *store.DB, http.Cookie, string) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	e = CreateAdmin(context.Background(), db, "admin@example.org", "long-test-password", "")
	if e != nil {
		t.Fatal(e)
	}
	a := New(db, send.New(db, testSender{}, send.Config{BaseURL: "https://example.org", TokenSecret: []byte("long-testing-token-secret-value")}), Config{})
	mux := http.NewServeMux()
	a.Register(mux)
	w := request(mux, "POST", "/api/v1/auth/login", `{"email":"admin@example.org","password":"long-test-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v map[string]any
	json.Unmarshal(w.Body.Bytes(), &v)
	return a, mux, db, *w.Result().Cookies()[0], v["csrf_token"].(string)
}
func request(m http.Handler, method, path, body string, c *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if c != nil {
		r.AddCookie(c)
	}
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w
}
func TestAuthenticationCSRFAndSecretStorage(t *testing.T) {
	_, m, db, c, csrf := setup(t)
	w := request(m, "GET", "/api/v1/brands", "", nil, "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = request(m, "POST", "/api/v1/brands", `{"name":"Brand","from_email":"from@example.org"}`, &c, "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = request(m, "POST", "/api/v1/brands", `{"name":"Brand","from_email":"from@example.org"}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var hash, csrfHash []byte
	if e := db.Reader.QueryRow("SELECT token_hash,csrf_hash FROM sessions").Scan(&hash, &csrfHash); e != nil {
		t.Fatal(e)
	}
	if string(hash) == c.Value || string(csrfHash) == csrf || !equal(hash, digest(c.Value)) {
		t.Fatal("raw secret storage")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie policy")
	}
}
func TestAPIIsolationUpsertAndPagination(t *testing.T) {
	_, m, db, c, csrf := setup(t)
	ctx := context.Background()
	s := contacts.New(db)
	b1, e := s.CreateBrand(ctx, contacts.Brand{Name: "one", FromEmail: "from@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	b2, e := s.CreateBrand(ctx, contacts.Brand{Name: "two", FromEmail: "from@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	w := request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"integration","scopes":["read","write"]}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var key map[string]any
	json.Unmarshal(w.Body.Bytes(), &key)
	bearer := key["secret"].(string)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w
	}
	w = do("POST", "/api/v1/subscribers", `{"email":"First@EXAMPLE.org","name":"first"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var first contacts.Subscriber
	json.Unmarshal(w.Body.Bytes(), &first)
	w = do("POST", "/api/v1/subscribers", `{"email":"first@example.org","name":"updated"}`)
	var updated contacts.Subscriber
	json.Unmarshal(w.Body.Bytes(), &updated)
	if w.Code != 200 || updated.ID != first.ID || updated.Name != "updated" {
		t.Fatal(w.Code, w.Body.String())
	}
	_, e = s.Upsert(ctx, contacts.UpsertInput{BrandID: b1.ID, Email: "second@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Upsert(ctx, contacts.UpsertInput{BrandID: b2.ID, Email: "private@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	w = do("GET", "/api/v1/subscribers?brand_id=2", "")
	if w.Code != 403 {
		t.Fatal("cross brand leak", w.Code)
	}
	w = do("GET", "/api/v1/subscribers?limit=1", "")
	var rows []contacts.Subscriber
	json.Unmarshal(w.Body.Bytes(), &rows)
	if w.Code != 200 || len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatal(w.Code, w.Body.String())
	}
	w = do("GET", "/api/v1/subscribers?after=1&limit=1", "")
	json.Unmarshal(w.Body.Bytes(), &rows)
	if w.Code != 200 || len(rows) != 1 || rows[0].Email != "second@example.org" {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestCampaignAPISendPipeline(t *testing.T) {
	a, m, db, c, csrf := setup(t)
	ctx := context.Background()
	s := contacts.New(db)
	b, e := s.CreateBrand(ctx, contacts.Brand{Name: "Brand", FromEmail: "from@example.org", TrackingDomain: "https://example.org"})
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.CreateList(ctx, contacts.List{BrandID: b.ID, Name: "Newsletter", OptIn: "single"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Upsert(ctx, contacts.UpsertInput{BrandID: b.ID, Email: "recipient@example.org", ListIDs: []int64{l.ID}})
	if e != nil {
		t.Fatal(e)
	}
	w := request(m, "POST", "/api/v1/campaigns?brand_id=1", `{"subject":"Welcome","html":"<p>Hello [Name,fallback=there]</p>"}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(m, "PUT", "/api/v1/campaigns/1/targets?brand_id=1", `[{"target_type":"list","target_id":1,"mode":"include"}]`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(m, "POST", "/api/v1/campaigns/1/start?brand_id=1", `{}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if e = a.engine.RunCampaign(runCtx, 1); e != nil {
		t.Fatal(e)
	}
	var sent int
	if e = db.Reader.QueryRow("SELECT sent FROM campaign_stats WHERE campaign_id=1").Scan(&sent); e != nil || sent != 1 {
		t.Fatal(sent, e)
	}
	w = request(m, "GET", "/api/v1/campaigns/1/report?brand_id=1", "", &c, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(m, "GET", "/api/v1/openapi.json", "", nil, "")
	var spec map[string]any
	if e = json.NewDecoder(io.NopCloser(w.Body)).Decode(&spec); e != nil {
		t.Fatal(e)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatal(spec)
	}
}
func TestReadOnlyKeyAndRevocation(t *testing.T) {
	_, m, db, c, csrf := setup(t)
	s := contacts.New(db)
	_, e := s.CreateBrand(context.Background(), contacts.Brand{Name: "one", FromEmail: "from@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	w := request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"readonly","scopes":["read"]}`, &c, csrf)
	var v map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	bearer := v["secret"].(string)
	do := func(method string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/subscribers", strings.NewReader(`{"email":"test@example.org"}`))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w
	}
	if w = do("GET"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = do("POST"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = request(m, "DELETE", "/api/v1/api-keys/1?brand_id=1", "", &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = do("GET"); w.Code != 401 {
		t.Fatal("revoked key authorized", w.Code)
	}
}
func TestTOTPRequired(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "totp.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = CreateAdmin(context.Background(), db, "admin@example.org", "long-test-password", "JBSWY3DPEHPK3PXP"); e != nil {
		t.Fatal(e)
	}
	a := New(db, nil, Config{SecureCookies: true})
	m := http.NewServeMux()
	a.Register(m)
	w := request(m, "POST", "/api/v1/auth/login", `{"email":"admin@example.org","password":"long-test-password"}`, nil, "")
	if w.Code != 401 {
		t.Fatal("TOTP bypass", w.Code)
	}
}
