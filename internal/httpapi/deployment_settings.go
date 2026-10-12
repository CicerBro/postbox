package httpapi

import (
	"net/http"
	"time"
)

// deploymentSettings exposes an explicit allowlist of environment-managed values.
// Credentials, local paths and provider internals never enter the response.
func (a *API) deploymentSettings(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, http.StatusOK, map[string]any{
		"base_url":                 a.cfg.BaseURL,
		"secure_cookies":           a.cfg.SecureCookies,
		"session_ttl_seconds":      int64(a.cfg.SessionTTL / time.Second),
		"turnstile_configured":     a.cfg.TurnstileSecret != "" && a.cfg.TurnstileSiteKey != "",
		"trusted_proxy_count":      len(a.cfg.TrustedProxies),
		"backups_configured":       a.cfg.BackupsConfigured,
		"raw_event_retention_days": a.cfg.RawEventRetentionDays,
		"max_send_workers":         a.cfg.MaxSendWorkers,
		"recovery_policy":          a.cfg.RecoveryPolicy,
		"soft_bounce_limit":        a.cfg.SoftBounceLimit,
		"geoip_configured":         a.cfg.GeoIPConfigured,
	})
}
