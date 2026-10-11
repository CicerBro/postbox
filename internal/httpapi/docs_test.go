package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"postbox/internal/subscribers"
	"postbox/internal/web"
)

func TestDocumentationRequiresLogin(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	mux.Handle("/", web.Handler())
	w := request(mux, "GET", "/docs", "", &cookie, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Postbox documentation") {
		t.Fatal(w.Code, w.Body.String())
	}
	assets := regexp.MustCompile(`(?:src|href)="(/docs/assets/[^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("documentation scripts and styles are not embedded")
	}
	paths := []string{"/docs", "/docs/", "/docs/api", "/docs/guides/authentication-and-sessions", "/docs/openapi.json", "/api/v1/openapi.json"}
	for _, asset := range assets {
		paths = append(paths, asset[1])
	}
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "docs", FromEmail: "docs@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	w = request(mux, "POST", "/api/v1/api-keys?brand_id="+strconv.FormatInt(brand.ID, 10), `{"name":"docs-test","scopes":["read"]}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var key struct {
		Key string `json:"secret"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil || key.Key == "" {
		t.Fatal("missing API key", err, w.Body.String())
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			for _, method := range []string{"GET", "HEAD"} {
				loggedIn := request(mux, method, p, "", &cookie, "")
				if loggedIn.Code != 200 {
					t.Fatal(method, loggedIn.Code, loggedIn.Body.String())
				}
				cache := "private, no-store"
				if strings.HasPrefix(loggedIn.Header().Get("Content-Type"), "text/html") {
					cache += ", no-transform"
				}
				if loggedIn.Header().Get("Cache-Control") != cache || !strings.Contains(loggedIn.Header().Get("Vary"), "Cookie") {
					t.Fatal("documentation may be cached outside the session")
				}
			}
			anonymous := request(mux, "GET", p, "", nil, "")
			if anonymous.Code != 401 && anonymous.Code != 303 {
				t.Fatal("anonymous documentation access", anonymous.Code)
			}
			r := httptest.NewRequest("GET", p, nil)
			r.Header.Set("Authorization", "Bearer "+key.Key)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 401 && w.Code != 303 {
				t.Fatal("API keys must not unlock documentation", w.Code)
			}
		})
	}
	// Even a non-administrator with no brand assignments can read the guides.
	if _, err := db.Writer.Exec("UPDATE users SET role='brand_user'"); err != nil {
		t.Fatal(err)
	}
	if w := request(mux, "GET", "/docs", "", &cookie, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// Expiration and revocation apply to pages, specs and every asset request.
	if _, err := db.Writer.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		w := request(mux, "GET", p, "", &cookie, "")
		if w.Code != 401 && w.Code != 303 {
			t.Fatal("expired session admitted", p, w.Code)
		}
	}
	if _, err := db.Writer.Exec("DELETE FROM sessions"); err != nil {
		t.Fatal(err)
	}
	if w := request(mux, "GET", "/docs/openapi.json", "", &cookie, ""); w.Code != 401 {
		t.Fatal("revoked session admitted", w.Code)
	}
}

func TestDocumentationSpecification(t *testing.T) {
	_, mux, _, cookie, _ := setup(t)
	w := request(mux, "GET", "/docs/openapi.json", "", &cookie, "")
	var spec struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			Tags       []string         `json:"tags"`
			Security   []map[string]any `json:"security"`
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil || spec.OpenAPI != "3.0.3" {
		t.Fatal(err, w.Body.String())
	}
	if len(spec.Paths["/api/v1/brands"]["get"].Tags) != 1 {
		t.Fatal("missing reference navigation groups")
	}
	op := spec.Paths["/api/v1/openapi.json"]["get"]
	if len(op.Security) != 1 || op.Security[0]["cookieAuth"] == nil {
		t.Fatal("spec download does not document session authentication")
	}
	found := false
	for _, p := range spec.Paths["/api/v1/brands/{brand}"]["delete"].Parameters {
		if p.Name == "X-CSRF-Token" {
			found = true
		}
	}
	if !found {
		t.Fatal("session deletes must document CSRF")
	}
}
