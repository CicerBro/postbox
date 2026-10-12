package htmlpolicy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolicyPermissions(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		policy                               Policy
		scripts, connections, sandbox, forms string
	}{
		{"app", App, "'self' https://challenges.cloudflare.com https://static.cloudflareinsights.com", "'self' https://challenges.cloudflare.com https://cloudflareinsights.com", "", "'self'"},
		{"signup", Signup, "https://challenges.cloudflare.com https://static.cloudflareinsights.com", "https://challenges.cloudflare.com https://cloudflareinsights.com", "", "'self'"},
		{"email", Email, "'none'", "'none'", "sandbox allow-popups allow-popups-to-escape-sandbox", "'none'"},
		{"unsubscribe preview", UnsubscribePreview, "'none'", "'none'", "sandbox allow-popups allow-popups-to-escape-sandbox allow-forms", "'self'"},
		{"preferences", Preferences, "'none'", "'none'", "", "'self'"},
		{"custom HTML", Custom, "'none'", "'none'", "sandbox", "'none'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directives := map[string]string{}
			for _, part := range strings.Split(string(tc.policy), ";") {
				fields := strings.Fields(part)
				if _, present := directives[fields[0]]; present {
					t.Fatal("duplicate directive", fields[0])
				}
				directives[fields[0]] = strings.Join(fields[1:], " ")
			}
			for directive, expected := range map[string]string{"script-src": tc.scripts, "connect-src": tc.connections, "form-action": tc.forms} {
				actual, present := directives[directive]
				if !present {
					actual = directives["default-src"]
				}
				if actual != expected {
					t.Fatalf("%s: got %q, want %q", directive, actual, expected)
				}
			}
			sandbox := ""
			if permissions, present := directives["sandbox"]; present {
				sandbox = strings.TrimSpace("sandbox " + permissions)
			}
			if sandbox != tc.sandbox {
				t.Fatalf("sandbox: got %q, want %q", sandbox, tc.sandbox)
			}
			if tc.policy == Signup && directives["frame-src"] != "https://challenges.cloudflare.com" {
				t.Fatal("Turnstile frame permission changed")
			}
			if tc.policy == App && directives["frame-src"] != "'self' about: https://challenges.cloudflare.com" {
				t.Fatal("app Turnstile frame permission missing")
			}
			if tc.policy == App && (directives["object-src"] != "'none'" || directives["frame-ancestors"] != "'none'" || directives["base-uri"] != "'self'") {
				t.Fatal("app security restrictions changed", directives)
			}
		})
	}
}

func TestProxyProtectionOnWire(t *testing.T) {
	for _, cache := range []string{"no-cache", "no-store", "private, no-store"} {
		t.Run(cache, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { Set(w, Email, cache) }))
			defer server.Close()
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.Header.Get("Cache-Control") != cache+", no-transform" || response.Header.Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal("missing HTML proxy protection", response.Header)
			}
			// A nonempty local rule set prevents the proxy adding its own URL.
			if values := response.Header.Values("Speculation-Rules"); len(values) != 1 || values[0] != `"data:application/speculationrules+json,%7B%7D"` {
				t.Fatal("missing explicit speculation opt-out", response.Header)
			}
		})
	}
}
