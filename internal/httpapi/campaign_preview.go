package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"net/http"
	"postbox/internal/previewtoken"
	"postbox/internal/subscribers"
	"strings"
)

func previewHeaders(w http.ResponseWriter, forms bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	sandbox, form := "sandbox", "'none'"
	if forms {
		sandbox += " allow-forms"
		form = "'self'"
	}
	w.Header().Set("Content-Security-Policy", sandbox+"; default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline' https: http:; base-uri 'none'; form-action "+form)
}

func (a *API) previewIdentity(w http.ResponseWriter, r *http.Request) (previewtoken.Claims, bool) {
	c, err := a.engine.PreviewIdentity(r.Context(), r.PathValue("token"))
	if err != nil || !strings.EqualFold(c.Host, r.Host) || c.List != 0 {
		http.NotFound(w, r)
		return c, false
	}
	return c, true
}

func (a *API) savedCampaignPreview(w http.ResponseWriter, r *http.Request) {
	c, ok := a.previewIdentity(w, r)
	if !ok {
		return
	}
	body, err := a.engine.SavedEditorPreview(r.Context(), c)
	if err != nil {
		http.Error(w, "Preview could not be rendered. Save the campaign and try again.", 400)
		return
	}
	previewHeaders(w, false)
	fmt.Fprint(w, body)
}

// This handler deliberately performs reads only, including form submissions.
func (a *API) unsubscribePreview(w http.ResponseWriter, r *http.Request) {
	c, ok := a.previewIdentity(w, r)
	if !ok {
		return
	}
	previewHeaders(w, true)
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid preview selection", 400)
			return
		}
		selected, err := a.engine.PreviewIdentity(r.Context(), r.PostForm.Get("selection"))
		if err != nil || selected.Brand != c.Brand || selected.Campaign != c.Campaign || selected.Subscriber != c.Subscriber || selected.Host != c.Host {
			http.Error(w, "Invalid preview selection", 400)
			return
		}
		label := "all lists"
		if selected.List > 0 {
			err = a.db.Reader.QueryRowContext(r.Context(), `SELECT l.name FROM lists l JOIN list_subscribers ls ON ls.list_id=l.id WHERE l.id=? AND l.brand_id=? AND ls.subscriber_id=?`, selected.List, c.Brand, c.Subscriber).Scan(&label)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		body, err := subscribers.NotificationSetting(r.Context(), a.db.Reader, c.Brand, selected.List, "goodbye_html")
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Preview unavailable", 503)
			return
		}
		if body == "" {
			body = "You have been unsubscribed."
		}
		fmt.Fprintf(w, `<!doctype html><title>Unsubscribe preview</title><p><strong>Preview only. No subscription was changed.</strong></p><p>On a live campaign, this would unsubscribe the selected subscriber from %s.</p>%s`, html.EscapeString(label), body)
		return
	}
	var email string
	if c.Subscriber > 0 {
		if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT email FROM subscribers WHERE id=? AND brand_id=?`, c.Subscriber, c.Brand).Scan(&email); err != nil {
			http.NotFound(w, r)
			return
		}
	} else {
		email = "preview@example.org"
	}
	goodbye, err := subscribers.NotificationSetting(r.Context(), a.db.Reader, c.Brand, 0, "goodbye_html")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Preview unavailable", 503)
		return
	}
	if goodbye == "" {
		goodbye = "<h1>Email preferences</h1>"
	}
	type choice struct{ token, name string }
	choices := []choice{{r.PathValue("token"), "all lists"}}
	rows, err := a.db.Reader.QueryContext(r.Context(), `SELECT l.id,l.name FROM lists l JOIN list_subscribers ls ON ls.list_id=l.id WHERE ls.subscriber_id=? AND l.brand_id=? ORDER BY l.id`, c.Subscriber, c.Brand)
	if err != nil {
		http.Error(w, "Preview unavailable", 503)
		return
	}
	for rows.Next() {
		var id int64
		var name string
		if err = rows.Scan(&id, &name); err != nil {
			break
		}
		var tok string
		tok, err = a.engine.PreviewListToken(c, id)
		if err != nil {
			break
		}
		choices = append(choices, choice{tok, name})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Preview unavailable", 503)
		return
	}
	fmt.Fprintf(w, `<!doctype html><title>Unsubscribe preview</title><p><strong>Preview only. No subscription will be changed.</strong></p><p>Email preferences for %s</p>%s<form method="post" action="%s">`, html.EscapeString(email), goodbye, html.EscapeString(r.URL.Path))
	for _, v := range choices {
		fmt.Fprintf(w, `<p><button name="selection" value="%s">Unsubscribe from %s</button></p>`, html.EscapeString(v.token), html.EscapeString(v.name))
	}
	fmt.Fprint(w, `</form>`)
}
