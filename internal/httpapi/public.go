package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"postbox/internal/contacts"
	"strings"
	"time"
)

var subscriptionForm = template.Must(template.New("subscribe").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Subscribe</title></head><body><main><h1>{{.Name}}</h1><form method="post"><label>Email <input type="email" name="email" required></label><label>Name <input name="name"></label><div hidden><input name="website" tabindex="-1" autocomplete="off"></div><label><input type="checkbox" name="consent" required> I consent to receive this newsletter.</label>{{if .SiteKey}}<div class="cf-turnstile" data-sitekey="{{.SiteKey}}"></div><script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>{{end}}<button>Subscribe</button></form></main></body></html>`))

func (a *API) registerPublic(m *http.ServeMux) {
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
	respond(w, 200, map[string]string{"hosted_url": strings.TrimRight(a.cfg.BaseURL, "/") + "/subscribe/" + id, "html": "<iframe src=\"" + template.HTMLEscapeString(strings.TrimRight(a.cfg.BaseURL, "/")+"/subscribe/"+id) + "\" title=\"Subscribe\"></iframe>"})
}
func (a *API) publicSubscribe(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.allow("subscribe:"+host, 20) {
		fail(w, 429, errors.New("too many subscription requests"))
		return
	}
	var list contacts.List
	e := a.db.Reader.QueryRowContext(r.Context(), "SELECT id,brand_id,name,opt_in FROM lists WHERE uuid=?", r.PathValue("uuid")).Scan(&list.ID, &list.BrandID, &list.Name, &list.OptIn)
	if e != nil {
		result(w, nil, e)
		return
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = subscriptionForm.Execute(w, map[string]string{"Name": list.Name, "SiteKey": a.cfg.TurnstileSiteKey})
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
	v, e := a.contacts().Upsert(r.Context(), contacts.UpsertInput{BrandID: list.BrandID, Email: r.FormValue("email"), Name: r.FormValue("name"), ListIDs: []int64{list.ID}, Consent: true, ConsentIP: host, Resubscribe: true})
	if e != nil {
		result(w, nil, e)
		return
	}
	status := "subscribed"
	if len(v.ConfirmationTokens) > 0 {
		status = "confirmation_pending"
	}
	respond(w, 200, map[string]string{"status": status})
}
func (a *API) verifyTurnstile(r *http.Request, host string) error {
	form := url.Values{"secret": {a.cfg.TurnstileSecret}, "response": {r.FormValue("cf-turnstile-response")}, "remoteip": {host}}
	req, e := http.NewRequestWithContext(r.Context(), "POST", "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	var v struct{ Success bool }
	if e = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&v); e != nil {
		return e
	}
	if res.StatusCode != 200 || !v.Success {
		return errors.New("Turnstile verification failed")
	}
	return nil
}
func (a *API) confirm(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.allow("confirm:"+host, 30) {
		fail(w, 429, errors.New("too many confirmation requests"))
		return
	}
	result(w, map[string]bool{"confirmed": true}, a.contacts().Confirm(r.Context(), r.PathValue("token")))
}
