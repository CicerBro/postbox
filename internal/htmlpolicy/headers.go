// Package htmlpolicy protects HTML responses from intermediary transformations.
package htmlpolicy

import "net/http"

type Policy string

const (
	beaconScript  = "https://static.cloudflareinsights.com"
	beaconConnect = "https://cloudflareinsights.com"
	sandboxLinks  = "sandbox allow-popups allow-popups-to-escape-sandbox"
	emailSources  = "default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline' https: http:; base-uri 'none'"

	App                Policy = "default-src 'self'; script-src 'self' https://challenges.cloudflare.com " + beaconScript + "; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self' https://challenges.cloudflare.com " + beaconConnect + "; frame-src 'self' about: https://challenges.cloudflare.com; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'"
	Signup             Policy = "default-src 'none'; img-src 'self'; script-src https://challenges.cloudflare.com " + beaconScript + "; frame-src https://challenges.cloudflare.com; connect-src https://challenges.cloudflare.com " + beaconConnect + "; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'"
	Email              Policy = sandboxLinks + "; " + emailSources + "; form-action 'none'"
	UnsubscribePreview Policy = sandboxLinks + " allow-forms; " + emailSources + "; form-action 'self'"
	Preferences        Policy = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' https: data:; form-action 'self'; base-uri 'none'"
	Custom             Policy = "sandbox; default-src 'none'; img-src 'self' https: data:; style-src 'unsafe-inline' https:; base-uri 'none'; form-action 'none'"
)

// Set serves HTML under a named policy with shared proxy protection.
func Set(w http.ResponseWriter, policy Policy, cacheControl string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", string(policy))
	Protect(w, cacheControl)
}

// Protect preserves the caller's caching policy while preventing proxy-injected
// scripts and speculative loading of links with application side effects.
func Protect(w http.ResponseWriter, cacheControl string) {
	w.Header().Set("Cache-Control", cacheControl+", no-transform")
	// Cloudflare replaces an empty header with Speed Brain rules. A nonempty
	// data URL supplies an empty rule set without a network request or CORS
	// failure from sandboxed documents, which have a null origin.
	w.Header().Set("Speculation-Rules", `"data:application/speculationrules+json,%7B%7D"`)
}
