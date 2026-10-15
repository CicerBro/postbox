package web

import (
	"net/http"
	"net/http/httptest"
	"postbox/internal/htmlpolicy"
	"regexp"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/", 200}, {"/subscribers", 200}, {"/contacts", 200}, {"/api-keys", 200}, {"/settings", 200}, {"/campaigns/42/report", 200},
		{"/account", 200}, {"/account/security", 200}, {"/account/preferences", 200},
		{"/admin", 200}, {"/admin/users", 200}, {"/admin/policies", 200}, {"/admin/operations", 200}, {"/admin/suppressions", 200}, {"/admin/deployment", 200},
		{"/brands/42/settings", 200}, {"/brands/42/settings/general", 200}, {"/brands/42/settings/sending", 200},
		{"/brands/42/settings/subscriptions", 200}, {"/brands/42/settings/integrations", 200}, {"/brands/42/settings/danger", 200},
		{"/api", 404}, {"/api/v1/unknown", 404}, {"/unknown", 404}, {"/assets/missing.js", 404},
		{"/docs", 404}, {"/docs/index.html", 404}, {"/docs/assets/postbox-campaigns.png", 404},
		{"/../index.html", 404}, {"/assets/../index.html", 404}, {"/campaigns/not-an-id", 404},
		{"/admin/unknown", 404}, {"/admin/policies/extra", 404}, {"/account/unknown", 404},
		{"/brands/not-an-id/settings/general", 404}, {"/brands//settings/general", 404},
		{"/brands/42", 404}, {"/brands/42/unknown", 404}, {"/brands/42/settings/unknown", 404}, {"/brands/42/settings/general/extra", 404},
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

func TestVersionedAppAssets(t *testing.T) {
	html, err := assets.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, pattern := range []string{
		`<script src="(/assets/theme-init-[^"]+\.js)"></script>`,
		`href="(/assets/favicon-[^"]+\.ico)"`,
		`href="(/assets/favicon-[^"]+\.svg)"`,
		`href="(/assets/index-[^"]+\.css)"`,
	} {
		match := regexp.MustCompile(pattern).FindSubmatch(html)
		if match == nil {
			t.Fatalf("missing versioned asset: %s", pattern)
		}
		paths = append(paths, string(match[1]))
	}
	css, err := assets.ReadFile("dist" + paths[len(paths)-1])
	if err != nil {
		t.Fatal(err)
	}
	font := regexp.MustCompile(`/assets/inter-latin-opsz-normal-[^\s)"']+\.woff2`).Find(css)
	if font == nil {
		t.Fatal("stylesheet must reference a versioned font")
	}
	paths = append(paths, string(font))
	for _, assetPath := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest(method, assetPath, nil))
			if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("%s %s: status %d, headers %v", method, assetPath, w.Code, w.Header())
			}
		}
	}
}

func TestHTMLProxyProtectionAndBeaconPolicy(t *testing.T) {
	for _, tc := range []struct {
		handler http.Handler
		path    string
		cache   string
		code    int
	}{
		{Handler(), "/", "no-cache, no-transform", 200},
		{Handler(), "/index.html", "no-cache, no-transform", 301},
		{Handler(), "/campaigns/42", "no-cache, no-transform", 200},
		{Handler(), "/admin/policies", "no-cache, no-transform", 200},
		{Handler(), "/account/security", "no-cache, no-transform", 200},
		{Handler(), "/brands/42/settings/general", "no-cache, no-transform", 200},
		{DocsHandler(), "/docs/", "private, no-store, no-transform", 200},
		{DocsHandler(), "/docs/api", "private, no-store, no-transform", 200},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+tc.path, func(t *testing.T) {
				w := httptest.NewRecorder()
				tc.handler.ServeHTTP(w, httptest.NewRequest(method, tc.path, nil))
				if w.Code != tc.code || w.Header().Get("Cache-Control") != tc.cache {
					t.Fatal("HTML proxy protection or caching changed", w.Code, w.Header())
				}
				if w.Header().Get("Content-Security-Policy") != string(htmlpolicy.App) {
					t.Fatal("incorrect app HTML policy", w.Header())
				}
			})
		}
	}
}
