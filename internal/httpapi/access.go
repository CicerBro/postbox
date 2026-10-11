package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// requestIdentity reads current credentials and permissions without charging
// another request to the rate limiter. Long-lived streams reuse it before updates.
func (a *API) requestIdentity(r *http.Request) (*identity, int, error) {
	i := &identity{}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		key := strings.TrimPrefix(h, "Bearer ")
		prefix, _, ok := strings.Cut(key, ".")
		var hash []byte
		var scopes string
		if !ok || a.db.Reader.QueryRowContext(r.Context(), "SELECT brand_id,secret_hash,scopes FROM api_keys WHERE prefix=? AND revoked_at IS NULL", prefix).Scan(&i.BrandID, &hash, &scopes) != nil || !equal(hash, digest(key)) {
			return nil, http.StatusUnauthorized, errors.New("invalid API key")
		}
		if err := json.Unmarshal([]byte(scopes), &i.Scopes); err != nil {
			return nil, http.StatusForbidden, errors.New("API key scope denied")
		}
		need := "read"
		if r.Method != "GET" {
			need = "write"
		}
		for _, scope := range i.Scopes {
			if scope == need || scope == "*" {
				return i, 0, nil
			}
		}
		return nil, http.StatusForbidden, errors.New("API key scope denied")
	}
	cookie, err := r.Cookie("postbox_session")
	var role string
	if err != nil || a.db.Reader.QueryRowContext(r.Context(), "SELECT s.user_id,u.role,s.csrf_hash FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?", digest(cookie.Value), time.Now().UnixMilli()).Scan(&i.UserID, &role, &i.CSRF) != nil {
		return nil, http.StatusUnauthorized, errors.New("login required")
	}
	i.Admin = role == "admin"
	if r.Method != "GET" && r.Method != "HEAD" && !equal(i.CSRF, digest(r.Header.Get("X-CSRF-Token"))) {
		return nil, http.StatusForbidden, errors.New("invalid CSRF token")
	}
	return i, 0, nil
}
