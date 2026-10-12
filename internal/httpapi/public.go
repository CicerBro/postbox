package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"html/template"
	"io"
	"net/http"
	"postbox/internal/emailpreferences"
	"postbox/internal/htmlpolicy"
	"postbox/internal/subscribers"
	"strings"
	"time"
)

var subscriptionForm = template.Must(template.New("subscribe").Parse(`<h1>{{.Name}}</h1>
<p class="muted">Subscribe to receive the latest updates in your inbox.</p>
<form method="post" class="subscription-form">
<label class="field">Email<input type="email" name="email" placeholder="you@example.com" autocomplete="email" required></label>
<label class="field">Name <span class="muted">(optional)</span><input name="name" placeholder="Your name" autocomplete="name"></label>
<div hidden><input name="website" tabindex="-1" autocomplete="off"></div>
<label class="consent"><input type="checkbox" name="consent" required><span>I consent to receive this newsletter.</span></label>
{{if .SiteKey}}<div class="cf-turnstile" data-sitekey="{{.SiteKey}}" data-size="flexible"></div><script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>{{end}}
<button class="primary" type="submit">Subscribe <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14m-6-6 6 6-6 6"/></svg></button>
</form>`))

func (a *API) registerPublic(m *http.ServeMux) {
	a.registerSubscriptionSettings(m)
	a.documentRoute("GET /subscribe/{uuid}")
	m.HandleFunc("GET /subscribe/{uuid}", a.publicSubscribe)
	a.documentRoute("POST /subscribe/{uuid}")
	m.HandleFunc("POST /subscribe/{uuid}", a.publicSubscribe)
	a.documentRoute("GET /confirm/{token}")
	m.HandleFunc("GET /confirm/{token}", a.confirm)
	a.route(m, "GET /api/v1/lists/{id}/form", a.form)
}
func (a *API) form(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	var name, id string
	e := a.db.Reader.QueryRowContext(r.Context(), "SELECT name,uuid FROM lists WHERE id=? AND brand_id=?", number(r.PathValue("id")), brand).Scan(&name, &id)
	if e != nil {
		result(w, nil, e)
		return
	}
	if !a.requirePublicSubscriptions(w, r, brand) {
		return
	}
	respond(w, 200, map[string]string{"hosted_url": strings.TrimRight(a.cfg.BaseURL, "/") + "/subscribe/" + id, "html": "<iframe src=\"" + template.HTMLEscapeString(strings.TrimRight(a.cfg.BaseURL, "/")+"/subscribe/"+id) + "\" title=\"Subscribe\"></iframe>"})
}
func (a *API) publicSubscribe(w http.ResponseWriter, r *http.Request) {
	host := a.clientIP(r)
	if !a.allow(a.rateKey(r, "subscribe", false), 20) {
		fail(w, 429, errors.New("too many subscription requests"))
		return
	}
	var list subscribers.List
	var brandName string
	e := a.db.Reader.QueryRowContext(r.Context(), "SELECT l.id,l.brand_id,l.name,l.opt_in,b.name FROM lists l JOIN brands b ON b.id=l.brand_id WHERE l.uuid=?", r.PathValue("uuid")).Scan(&list.ID, &list.BrandID, &list.Name, &list.OptIn, &brandName)
	if e != nil {
		result(w, nil, e)
		return
	}
	if !a.requirePublicSubscriptions(w, r, list.BrandID) {
		return
	}
	if r.Method == "GET" {
		htmlpolicy.Set(w, htmlpolicy.Signup, "no-store")
		emailpreferences.Start(w, "Subscribe to "+list.Name, brandName)
		_ = subscriptionForm.Execute(w, map[string]string{"Name": list.Name, "SiteKey": a.cfg.TurnstileSiteKey})
		emailpreferences.End(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if e = r.ParseForm(); e != nil {
		result(w, nil, e)
		return
	}
	if r.FormValue("website") != "" {
		respond(w, 200, map[string]string{"status": "received"})
		return
	}
	if r.FormValue("consent") == "" {
		fail(w, 400, errors.New("consent is required"))
		return
	}
	if a.cfg.TurnstileSecret != "" {
		if e = a.verifyTurnstile(r, host); e != nil {
			fail(w, 400, e)
			return
		}
	}
	v, e := a.subscriberService().PublicSubscribe(r.Context(), subscribers.UpsertInput{BrandID: list.BrandID, Email: r.FormValue("email"), Name: r.FormValue("name"), ListIDs: []int64{list.ID}, Consent: true, ConsentIP: host, Resubscribe: true})
	if e != nil {
		result(w, nil, e)
		return
	}
	status := "subscribed"
	if len(v.ConfirmationTokens) > 0 {
		status = "confirmation_pending"
	}
	if wantsHostedHTML(r) {
		a.thankyou(w, r, list.BrandID, list.ID, status)
		return
	}
	respond(w, 200, map[string]string{"status": status})
}
func (a *API) confirm(w http.ResponseWriter, r *http.Request) {
	if !a.allow(a.rateKey(r, "confirm", false), 30) {
		fail(w, 429, errors.New("too many confirmation requests"))
		return
	}
	var brand, listID int64
	hash := sha256.Sum256([]byte(r.PathValue("token")))
	e := a.db.Reader.QueryRowContext(r.Context(), "SELECT l.brand_id,l.id FROM subscription_confirmations sc JOIN lists l ON l.id=sc.list_id WHERE sc.token_hash=? AND sc.expires_at>?", hash[:], time.Now().UnixMilli()).Scan(&brand, &listID)
	if errors.Is(e, sql.ErrNoRows) {
		e = errors.New("confirmation token invalid or expired")
	}
	if e == nil {
		e = a.subscriberService().Confirm(r.Context(), r.PathValue("token"))
	}
	if e != nil {
		result(w, nil, e)
		return
	}
	if wantsHostedHTML(r) {
		a.thankyou(w, r, brand, listID, "confirmed")
		return
	}
	result(w, map[string]bool{"confirmed": true}, nil)
}

func wantsHostedHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html") && !strings.Contains(r.Header.Get("Accept"), "application/json")
}
func (a *API) thankyou(w http.ResponseWriter, r *http.Request, brand, listID int64, status string) {
	name := "thankyou_html"
	if status == "confirmation_pending" {
		name = "confirmation_page_html"
	}
	body, e := subscribers.NotificationSetting(r.Context(), a.db.Reader, brand, listID, name)
	if name == "confirmation_page_html" && errors.Is(e, sql.ErrNoRows) {
		body, e = subscribers.NotificationSetting(r.Context(), a.db.Reader, brand, listID, "thankyou_html")
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		result(w, nil, e)
		return
	}
	if body == "" {
		message := "Thank you for subscribing."
		if status == "confirmation_pending" {
			message = "Please check your email to confirm your subscription."
		}
		if status == "confirmed" {
			message = "Your subscription is confirmed."
		}
		var brandName string
		if e := a.db.Reader.QueryRowContext(r.Context(), "SELECT name FROM brands WHERE id=?", brand).Scan(&brandName); e != nil {
			result(w, nil, e)
			return
		}
		htmlpolicy.Set(w, htmlpolicy.Custom, "no-store")
		emailpreferences.Confirmation(w, "Thank you", message, brandName)
		return
	}
	htmlpolicy.Set(w, htmlpolicy.Custom, "no-store")
	_, _ = io.WriteString(w, body)
}
