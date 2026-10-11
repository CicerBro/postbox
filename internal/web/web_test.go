package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/", 200}, {"/subscribers", 200}, {"/contacts", 200}, {"/api-keys", 200}, {"/settings", 200}, {"/campaigns/42/report", 200},
		{"/api", 404}, {"/api/v1/unknown", 404}, {"/unknown", 404}, {"/assets/missing.js", 404},
		{"/docs", 404}, {"/docs/index.html", 404}, {"/docs/assets/postbox-campaigns.png", 404},
		{"/../index.html", 404}, {"/assets/../index.html", 404}, {"/campaigns/not-an-id", 404},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("status %d, want %d", w.Code, tc.code)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing nosniff")
			}
			if tc.code == 200 && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("SPA MIME is not HTML")
			}
		})
	}
}
func TestStaticCaching(t *testing.T) {
	entries, err := assets.ReadDir("dist/assets")
	if err != nil {
		t.Skip("no built assets")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest("GET", "/assets/"+entry.Name(), nil))
		if w.Code != 200 {
			t.Fatalf("asset %s: %d", entry.Name(), w.Code)
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Fatal("asset not immutable")
		}
		if strings.HasSuffix(entry.Name(), ".js") && !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
			t.Fatal("wrong JavaScript MIME")
		}
	}
}
