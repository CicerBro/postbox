package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"postbox/internal/htmlpolicy"
	"strings"

	"postbox/internal/web"
)

func (a *API) registerDocs(m *http.ServeMux) {
	h := a.docsSession(web.DocsHandler(), true)
	m.Handle("/docs", h)
	m.Handle("/docs/", h)
	m.Handle("GET /docs/openapi.json", a.docsSession(http.HandlerFunc(a.openapi), false))
}

// Documentation requires a browser login, including its bundled assets and spec.
func (a *API) docsSession(next http.Handler, redirect bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		i, status, err := a.requestIdentity(r)
		if err == nil && i.UserID == 0 {
			status, err = http.StatusUnauthorized, errors.New("login required")
		}
		if err != nil {
			if redirect && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.Contains(r.URL.Path, "/assets/") {
				htmlpolicy.Protect(w, "private, no-store")
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			} else {
				fail(w, status, err)
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, i)))
	})
}
