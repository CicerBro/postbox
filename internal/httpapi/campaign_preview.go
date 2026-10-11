package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"net/http"
	"postbox/internal/emailpreferences"
	"postbox/internal/htmlpolicy"
	"postbox/internal/previewtoken"
	"postbox/internal/subscribers"
	"strings"
)

func previewHeaders(w http.ResponseWriter, forms bool) {
	policy := htmlpolicy.Email
	if forms {
		policy = htmlpolicy.UnsubscribePreview
	}
	htmlpolicy.Set(w, policy, "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func (a *API) previewIdentity(w http.ResponseWriter, r *http.Request) (previewtoken.Claims, bool) {
	c, err := a.engine.PreviewIdentity(r.Context(), r.PathValue("token"))
	if err != nil || !strings.EqualFold(c.Host, r.Host) || c.List != 0 || c.Scope != "" {
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
	var brandName string
	if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT name FROM brands WHERE id=?`, c.Brand).Scan(&brandName); err != nil {
		http.Error(w, "Preview unavailable", 503)
		return
	}
	lists, err := emailpreferences.CurrentMailingLists(r.Context(), a.db.Reader, c.Campaign, c.Subscriber)
	if err != nil {
		http.Error(w, "Preview unavailable", 503)
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
		label := "all emails from this sender"
		if selected.Scope == "current" {
			if len(lists) == 0 {
				http.Error(w, "Invalid preview selection", 400)
				return
			}
			label = "the lists associated with this email"
			if len(lists) == 1 {
				selected.List = lists[0]
				label = "this list"
			}
		} else if selected.List > 0 {
			var exists int
			err = a.db.Reader.QueryRowContext(r.Context(), `SELECT 1 FROM lists l JOIN list_subscribers ls ON ls.list_id=l.id WHERE l.id=? AND l.brand_id=? AND ls.subscriber_id=?`, selected.List, c.Brand, c.Subscriber).Scan(&exists)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			label = "this list"
		}
		body, err := subscribers.NotificationSetting(r.Context(), a.db.Reader, c.Brand, selected.List, "goodbye_html")
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Preview unavailable", 503)
			return
		}
		emailpreferences.Start(w, "Unsubscribe preview", brandName)
		fmt.Fprint(w, `<p class="muted"><strong>Preview only. No subscription was changed.</strong></p>`)
		if body == "" {
			fmt.Fprintf(w, `<h1>You're unsubscribed</h1><p class="muted">On a live campaign, this would unsubscribe you from %s.</p>`, html.EscapeString(label))
		} else {
			fmt.Fprint(w, body)
		}
		emailpreferences.End(w)
		return
	}
	goodbye, err := subscribers.NotificationSetting(r.Context(), a.db.Reader, c.Brand, 0, "goodbye_html")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Preview unavailable", 503)
		return
	}
	if goodbye == "" {
		goodbye = `<h1>Unsubscribe</h1><p class="muted">Stop receiving emails from this sender.</p>`
	}
	var currentToken string
	if len(lists) > 0 {
		current := c
		current.Scope = "current"
		currentToken, err = a.engine.PreviewListToken(current, 0)
		if err != nil {
			http.Error(w, "Preview unavailable", 503)
			return
		}
	}
	emailpreferences.Start(w, "Unsubscribe preview", brandName)
	fmt.Fprint(w, `<p class="muted"><strong>Preview only. No subscription will be changed.</strong></p>`+goodbye+`<form method="post"><div class="actions">`)
	if len(lists) > 0 {
		label := "Unsubscribe from this list"
		if len(lists) > 1 {
			label = "Unsubscribe from these lists"
		}
		fmt.Fprintf(w, `<button class="primary" name="selection" value="%s">%s</button><button name="selection" value="%s">Unsubscribe from all emails</button>`, html.EscapeString(currentToken), label, html.EscapeString(r.PathValue("token")))
	} else {
		fmt.Fprintf(w, `<button class="primary" name="selection" value="%s">Unsubscribe from all emails</button>`, html.EscapeString(r.PathValue("token")))
	}
	fmt.Fprint(w, `</div></form>`)
	emailpreferences.End(w)
}
