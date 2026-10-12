package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (a *API) authTurnstileRequired() bool {
	return a.cfg.TurnstileSecret != "" || a.cfg.TurnstileSiteKey != ""
}

func (a *API) authConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	respond(w, http.StatusOK, map[string]any{
		"turnstile_required": a.authTurnstileRequired(),
		"turnstile_site_key": a.cfg.TurnstileSiteKey,
	})
}

func (a *API) requireAuthTurnstile(w http.ResponseWriter, r *http.Request, token, action string) bool {
	if !a.authTurnstileRequired() {
		return true
	}
	if a.cfg.TurnstileSiteKey == "" || a.cfg.TurnstileSecret == "" {
		fail(w, http.StatusServiceUnavailable, errors.New("sign-in verification is not configured correctly"))
		return false
	}
	if err := a.verifyTurnstileToken(r, token, a.clientIP(r), action); err != nil {
		fail(w, http.StatusForbidden, errors.New("security verification failed; please try again"))
		return false
	}
	return true
}

func (a *API) verifyTurnstile(r *http.Request, host string) error {
	return a.verifyTurnstileToken(r, r.FormValue("cf-turnstile-response"), host, "")
}

func (a *API) verifyTurnstileToken(r *http.Request, token, host, action string) error {
	if strings.TrimSpace(token) == "" || len(token) > 2048 {
		return errors.New("Turnstile verification failed")
	}
	form := url.Values{"secret": {a.cfg.TurnstileSecret}, "response": {token}, "remoteip": {host}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.turnstileClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var v struct {
		Success          bool
		Hostname, Action string
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&v); err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || !v.Success {
		return errors.New("Turnstile verification failed")
	}
	// Auth tokens must come from this deployment and the login widget, not
	// from a subscriber form or another site using the same Turnstile key.
	if action != "" {
		base, err := url.Parse(a.cfg.BaseURL)
		if err != nil || base.Hostname() == "" || !strings.EqualFold(v.Hostname, base.Hostname()) || v.Action != action {
			return errors.New("Turnstile verification failed")
		}
	}
	return nil
}
