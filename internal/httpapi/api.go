package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"postbox/internal/send"
	"postbox/internal/store"
)

type Config struct {
	SecureCookies                              bool
	SessionTTL                                 time.Duration
	BaseURL, TurnstileSecret, TurnstileSiteKey string
}
type API struct {
	passkeys *webauthn.WebAuthn
	db       *store.DB
	engine   *send.Engine
	cfg      Config
	mu       sync.Mutex
	limits   map[string]*bucket
	paths    map[string]any
}
type bucket struct {
	n     int
	until time.Time
}
type identity struct {
	UserID, BrandID int64
	Admin           bool
	Scopes          []string
	CSRF            []byte
}
type identityKey struct{}

func New(db *store.DB, engine *send.Engine, cfg Config) *API {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	rp, _ := passkeyRP(cfg.BaseURL)
	return &API{passkeys: rp, db: db, engine: engine, cfg: cfg, limits: map[string]*bucket{}, paths: map[string]any{}}
}
func (a *API) route(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	a.documentRoute(pattern)
	mux.Handle(pattern, a.authenticate(h))
}
func (a *API) Register(m *http.ServeMux) {
	a.documentRoute("POST /api/v1/auth/login")
	m.HandleFunc("POST /api/v1/auth/login", a.login)
	a.route(m, "GET /api/v1/auth/session", a.session)
	a.route(m, "POST /api/v1/auth/logout", a.logout)
	a.documentRoute("GET /api/v1/openapi.json")
	m.HandleFunc("GET /api/v1/openapi.json", a.openapi)
	a.route(m, "GET /api/v1/brands", a.brands)
	a.route(m, "POST /api/v1/brands", a.brands)
	a.route(m, "GET /api/v1/brands/{brand}", a.brand)
	a.route(m, "PATCH /api/v1/brands/{brand}", a.brand)
	a.route(m, "DELETE /api/v1/brands/{brand}", a.brand)
	a.route(m, "GET /api/v1/overview", a.overview)
	a.route(m, "GET /api/v1/api-keys", a.keys)
	a.route(m, "POST /api/v1/api-keys", a.keys)
	a.route(m, "DELETE /api/v1/api-keys/{id}", a.keys)
	a.registerUsers(m)
	a.registerPasskeys(m)
	a.registerCampaigns(m)
	a.registerSubscribers(m)
	a.registerOperations(m)
	a.registerWebhooks(m)
	a.registerPublic(m)
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, err error) {
	respond(w, status, map[string]any{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, fmt.Errorf("invalid JSON: %w", err))
		return false
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		fail(w, 400, errors.New("request must contain one JSON value"))
		return false
	}
	return true
}
func secret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) []byte { b := sha256.Sum256([]byte(s)); return b[:] }
func number(s string) int64  { n, _ := strconv.ParseInt(s, 10, 64); return n }
func page(r *http.Request) (int64, int) {
	n := number(r.URL.Query().Get("limit"))
	if n < 1 || n > 500 {
		n = 100
	}
	return number(r.URL.Query().Get("after")), int(n)
}
func (a *API) allow(key string, max int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	b := a.limits[key]
	if b == nil || now.After(b.until) {
		b = &bucket{until: now.Add(time.Minute)}
		a.limits[key] = b
	}
	if b.n >= max {
		return false
	}
	b.n++
	if len(a.limits) > 10000 {
		for k, v := range a.limits {
			if now.After(v.until) {
				delete(a.limits, k)
			}
		}
	}
	return true
}
func (a *API) brandID(r *http.Request) (int64, error) {
	id := number(r.URL.Query().Get("brand_id"))
	if s := r.PathValue("brand"); s != "" {
		id = number(s)
	}
	i := r.Context().Value(identityKey{}).(*identity)
	if id <= 0 {
		id = i.BrandID
	}
	if id <= 0 {
		return 0, errors.New("brand_id is required")
	}
	if i.Admin {
		return id, nil
	}
	if i.BrandID != 0 {
		if id != i.BrandID {
			return 0, errors.New("brand access denied")
		}
		return id, nil
	}
	var perms string
	if err := a.db.Reader.QueryRowContext(r.Context(), "SELECT permissions FROM user_brands WHERE user_id=? AND brand_id=?", i.UserID, id).Scan(&perms); err != nil {
		return 0, errors.New("brand access denied")
	}
	var scopes []string
	_ = json.Unmarshal([]byte(perms), &scopes)
	needed := userRequestScope(r)
	for _, p := range scopes {
		if p == needed {
			return id, nil
		}
	}
	return 0, errors.New("permission denied")
}
func (a *API) scoped(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := a.brandID(r)
	if err != nil {
		fail(w, 403, err)
		return 0, false
	}
	return id, true
}
func queryRows(ctx context.Context, db *sql.DB, q string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		v := make([]any, len(cols))
		p := make([]any, len(cols))
		for j := range v {
			p[j] = &v[j]
		}
		if err = rows.Scan(p...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for j, c := range cols {
			if b, ok := v[j].([]byte); ok {
				v[j] = string(b)
			}
			m[c] = v[j]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func result(w http.ResponseWriter, v any, err error) {
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, errors.New("not found"))
		} else {
			fail(w, 400, err)
		}
		return
	}
	respond(w, 200, v)
}
func requireChanged(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var i identity
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key := strings.TrimPrefix(h, "Bearer ")
			prefix, _, ok := strings.Cut(key, ".")
			var hash []byte
			var scopes string
			if !ok || a.db.Reader.QueryRowContext(r.Context(), "SELECT brand_id,secret_hash,scopes FROM api_keys WHERE prefix=? AND revoked_at IS NULL", prefix).Scan(&i.BrandID, &hash, &scopes) != nil || !equal(hash, digest(key)) {
				fail(w, 401, errors.New("invalid API key"))
				return
			}
			_ = json.Unmarshal([]byte(scopes), &i.Scopes)
			need := "read"
			if r.Method != "GET" {
				need = "write"
			}
			allowed := false
			for _, s := range i.Scopes {
				if s == need || s == "*" {
					allowed = true
				}
			}
			if !allowed {
				fail(w, 403, errors.New("API key scope denied"))
				return
			}
			if !a.allow("api:"+prefix, 600) {
				fail(w, 429, errors.New("API rate limit exceeded"))
				return
			}
		} else {
			cookie, err := r.Cookie("postbox_session")
			var role string
			if err != nil || a.db.Reader.QueryRowContext(r.Context(), "SELECT s.user_id,u.role,s.csrf_hash FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?", digest(cookie.Value), time.Now().UnixMilli()).Scan(&i.UserID, &role, &i.CSRF) != nil {
				fail(w, 401, errors.New("login required"))
				return
			}
			i.Admin = role == "admin"
			if r.Method != "GET" && r.Method != "HEAD" && !equal(i.CSRF, digest(r.Header.Get("X-CSRF-Token"))) {
				fail(w, 403, errors.New("invalid CSRF token"))
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, &i))
		next.ServeHTTP(w, r)
	})
}
