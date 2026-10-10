package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
	"net/http"
	"postbox/internal/store"
	"strings"
	"time"
	"unicode/utf8"
)

type SessionResponse struct {
	UserID            int64  `json:"user_id"`
	Admin             bool   `json:"admin"`
	CSRFToken         string `json:"csrf_token"`
	TOTPEnabled       bool   `json:"totp_enabled"`
	PasskeysEnabled   bool   `json:"passkeys_enabled"`
	PasskeysAvailable bool   `json:"passkeys_available"`
	PasskeyCount      int    `json:"passkey_count"`
}

type LoginResponse struct {
	UserID    int64  `json:"user_id"`
	CSRFToken string `json:"csrf_token"`
	ExpiresAt int64  `json:"expires_at"`
}

type LogoutResponse struct {
	LoggedOut bool `json:"logged_out"`
}

func equal(a, b []byte) bool { return len(a) > 0 && subtle.ConstantTimeCompare(a, b) == 1 }
func HashPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < 8 {
		return "", errors.New("password must have at least 8 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func verifyPassword(encoded, password string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 6 || p[1] != "argon2id" || p[2] != "v=19" || p[3] != "m=65536,t=3,p=2" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	key, e := base64.RawStdEncoding.DecodeString(p[5])
	if e != nil || len(key) != 32 {
		return false
	}
	return equal(key, argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32))
}
func CreateAdmin(ctx context.Context, db *store.DB, email, password, totpSecret string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return errors.New("valid email required")
	}
	if totpSecret != "" {
		if _, err := totp.GenerateCode(totpSecret, time.Now()); err != nil {
			return errors.New("invalid TOTP secret")
		}
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO users(email,password_hash,totp_secret,role,created_at) VALUES(?,?,NULLIF(?,''),'admin',?)", email, hash, totpSecret, time.Now().UnixMilli())
		return err
	})
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if !a.allow(a.rateKey(r, "login", false), 10) {
		fail(w, 429, errors.New("too many login attempts"))
		return
	}
	var in struct{ Email, Password, TOTP string }
	if !decode(w, r, &in) {
		return
	}
	var id int64
	var hash string
	var otp sql.NullString
	err := a.db.Reader.QueryRowContext(r.Context(), "SELECT id,password_hash,totp_secret FROM users WHERE email=?", strings.ToLower(strings.TrimSpace(in.Email))).Scan(&id, &hash, &otp)
	if err != nil || !verifyPassword(hash, in.Password) || (otp.Valid && otp.String != "" && !totp.Validate(in.TOTP, otp.String)) {
		fail(w, 401, errors.New("invalid credentials"))
		return
	}
	token := secret()
	csrf := sessionCSRF(token)
	expires := time.Now().Add(a.cfg.SessionTTL)
	err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		return insertSession(r.Context(), c, id, token, csrf, expires)
	})
	if err != nil {
		fail(w, 500, err)
		return
	}
	a.sessionResponse(w, id, token, csrf, expires)
}
func (a *API) session(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("session authentication required"))
		return
	}
	cookie, _ := r.Cookie("postbox_session")
	csrf := sessionCSRF(cookie.Value)
	var otp bool
	var count int
	if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT totp_secret IS NOT NULL,(SELECT count(*) FROM passkeys WHERE user_id=users.id) FROM users WHERE id=?`, i.UserID).Scan(&otp, &count); err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, SessionResponse{UserID: i.UserID, Admin: i.Admin, CSRFToken: csrf, TOTPEnabled: otp, PasskeysEnabled: count > 0, PasskeysAvailable: a.passkeys != nil, PasskeyCount: count})
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("postbox_session")
	if err != nil {
		fail(w, 400, err)
		return
	}
	_, err = a.db.Writer.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", digest(c.Value))
	http.SetCookie(w, &http.Cookie{Name: "postbox_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	result(w, LogoutResponse{LoggedOut: true}, err)
}

// sessionCSRF derives an independent token from the high entropy session secret.
// The domain prefix separates it from the stored session hash. Reads in other
// tabs or API restarts return the same token, and a new session rotates it.
func sessionCSRF(token string) string {
	value := sha256.Sum256([]byte("postbox:session-csrf:v1:" + token))
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func insertSession(ctx context.Context, c *sql.Conn, id int64, token, csrf string, expires time.Time) error {
	_, err := c.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(?,?,?,?)", digest(token), id, expires.UnixMilli(), digest(csrf))
	return err
}
func (a *API) sessionResponse(w http.ResponseWriter, id int64, token, csrf string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: "postbox_session", Value: token, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: expires})
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, LoginResponse{UserID: id, CSRFToken: csrf, ExpiresAt: expires.UnixMilli()})
}
