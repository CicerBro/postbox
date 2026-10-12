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
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"postbox/internal/awsbrand"
	"postbox/internal/httplog"
	"postbox/internal/send"
	"postbox/internal/store"
)

type Config struct {
	TrustedProxies                                         []netip.Prefix
	AWSProvider                                            *awsbrand.Provider
	SecureCookies                                          bool
	SessionTTL                                             time.Duration
	BaseURL, TurnstileSecret, TurnstileSiteKey             string
	BackupsConfigured, GeoIPConfigured                     bool
	RawEventRetentionDays, MaxSendWorkers, SoftBounceLimit int
	RecoveryPolicy                                         string
}
type API struct {
	turnstileClient  *http.Client
	passkeys         *webauthn.WebAuthn
	db               *store.DB
	engine           *send.Engine
	cfg              Config
	mu               sync.Mutex
	limits           map[string]*bucket
	nextLimitCleanup time.Time
	paths            map[string]any
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
	return &API{turnstileClient: &http.Client{Timeout: 10 * time.Second}, passkeys: rp, db: db, engine: engine, cfg: cfg, limits: map[string]*bucket{}, paths: map[string]any{}}
}
func (a *API) route(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	a.documentRoute(pattern)
	mux.Handle(pattern, a.authenticate(h))
}
func (a *API) Register(m *http.ServeMux) {
	a.documentRoute("GET /api/v1/auth/config")
	m.HandleFunc("GET /api/v1/auth/config", a.authConfig)
	a.documentRoute("POST /api/v1/auth/login")
	m.HandleFunc("POST /api/v1/auth/login", a.login)
	a.route(m, "GET /api/v1/auth/session", a.session)
	a.route(m, "PATCH /api/v1/auth/preferences", a.preferences)
	a.route(m, "POST /api/v1/auth/logout", a.logout)
	a.documentRoute("GET /api/v1/openapi.json")
	m.Handle("GET /api/v1/openapi.json", a.docsSession(http.HandlerFunc(a.openapi), false))
	a.registerDocs(m)
	a.route(m, "GET /api/v1/brands", a.brands)
	a.route(m, "POST /api/v1/brands", a.brands)
	a.route(m, "GET /api/v1/brands/{brand}", a.brand)
	a.route(m, "PATCH /api/v1/brands/{brand}", a.brand)
	a.route(m, "DELETE /api/v1/brands/{brand}", a.brand)
	a.route(m, "GET /api/v1/overview", a.overview)
	a.route(m, "GET /api/v1/deployment-settings", a.deploymentSettings)
	a.route(m, "GET /api/v1/api-keys", a.keys)
	a.route(m, "POST /api/v1/api-keys", a.keys)
	a.route(m, "DELETE /api/v1/api-keys/{id}", a.keys)
	a.registerUsers(m)
	a.registerPasskeys(m)
	a.registerCampaigns(m)
	a.registerSubscribers(m)
	a.registerOperations(m)
	a.registerJobSettings(m)
	a.registerAWSSettings(m)
	a.registerWebhooks(m)
	a.registerPublic(m)
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		httplog.RecordError(w, err)
	}
}
func fail(w http.ResponseWriter, status int, err error) {
	if status >= 500 {
		httplog.RecordError(w, err)
	}
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
		i, status, err := a.requestIdentity(r)
		if err != nil {
			fail(w, status, err)
			return
		}
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			prefix, _, _ := strings.Cut(strings.TrimPrefix(h, "Bearer "), ".")
			if !a.allow("api:"+prefix, 600) {
				fail(w, 429, errors.New("API rate limit exceeded"))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, i)))
	})
}
