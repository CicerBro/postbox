package web

import (
	"io/fs"
	"net/http"
	"path"
	"postbox/internal/htmlpolicy"
	"strings"
)

// DocsHandler serves a separate embedded build. The API wraps it in session auth.
func DocsHandler() http.Handler {
	root, _ := fs.Sub(assets, "dist/docs")
	files := http.StripPrefix("/docs/", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		w.Header().Set("Cache-Control", "private, no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p := r.URL.Path
		if strings.ContainsAny(p, "\\\x00") || (path.Clean(p) != p && p != "/docs/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(p, "/docs/")
		if strings.HasPrefix(name, "assets/") {
			if info, err := fs.Stat(root, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if p != "/docs" && p != "/docs/" && p != "/docs/api" && !strings.HasPrefix(p, "/docs/guides/") {
			http.NotFound(w, r)
			return
		}
		content, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "Documentation unavailable; build the frontend", http.StatusInternalServerError)
			return
		}
		htmlpolicy.Set(w, htmlpolicy.App, "private, no-store")
		if r.Method != http.MethodHead {
			_, _ = w.Write(content)
		}
	})
}
