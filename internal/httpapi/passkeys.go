package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
)

// The relying party is deployment configuration, never the untrusted request Host.
func passkeyRP(base string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("passkeys require an absolute base URL without a path")
	}
	host := strings.ToLower(u.Hostname())
	local := host == "localhost"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("passkeys require HTTPS except on localhost")
	}
	if strings.HasSuffix(host, ".") || strings.ContainsAny(host, " /\\") || (!local && net.ParseIP(host) != nil) {
		return nil, errors.New("invalid passkey relying party")
	}
	return webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "Postbox", RPOrigins: []string{u.Scheme + "://" + strings.ToLower(u.Host)}, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(), UserVerification: protocol.VerificationRequired}})
}

type passkeyUser struct {
	id          int64
	handle      []byte
	email       string
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.email }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.email }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type passkeyQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (a *API) loadPasskeyUser(ctx context.Context, q passkeyQuerier, id int64) (*passkeyUser, error) {
	u := &passkeyUser{id: id}
	if err := q.QueryRowContext(ctx, `SELECT u.email,p.handle FROM users u JOIN passkey_users p ON p.user_id=u.id WHERE u.id=?`, id).Scan(&u.email, &u.handle); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT credential FROM passkeys WHERE user_id=? AND rp_id=?`, id, a.passkeys.Config.RPID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var c webauthn.Credential
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		u.credentials = append(u.credentials, c)
	}
	return u, rows.Err()
}
func (a *API) registerPasskeys(m *http.ServeMux) {
	for _, route := range []struct {
		p      string
		h      http.HandlerFunc
		public bool
	}{
		{"POST /api/v1/auth/passkeys/register/begin", a.beginPasskeyRegistration, false},
		{"POST /api/v1/auth/passkeys/register/finish", a.finishPasskeyRegistration, false},
		{"POST /api/v1/auth/passkeys/login/begin", a.beginPasskeyLogin, true},
		{"POST /api/v1/auth/passkeys/login/finish", a.finishPasskeyLogin, true},
		{"GET /api/v1/auth/passkeys", a.listPasskeys, false},
		{"DELETE /api/v1/auth/passkeys/{id}", a.deletePasskey, false},
	} {
		if route.public {
			a.documentRoute(route.p)
			m.HandleFunc(route.p, route.h)
		} else {
			a.route(m, route.p, route.h)
		}
	}
	a.documentPasskeys()
}
func (a *API) passkeyAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.passkeys == nil {
		fail(w, 503, errors.New("passkeys unavailable: configure a trusted HTTPS base URL"))
		return false
	}
	// Browser POSTs carry Origin. Reject same-site sibling-origin requests as well
	// as cross-site requests, before issuing or consuming a browser ceremony.
	if origin := r.Header.Get("Origin"); origin != "" && origin != a.passkeys.Config.RPOrigins[0] {
		fail(w, 403, errors.New("invalid request origin"))
		return false
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.allow("passkey:"+host, 20) {
		fail(w, 429, errors.New("too many authentication attempts"))
		return false
	}
	return true
}

// Both existing password and enrolled OTP are needed to change the factor set.
func (a *API) passkeyStepUp(w http.ResponseWriter, r *http.Request, password, code string) ([]byte, bool) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("session authentication required"))
		return nil, false
	}
	if !a.allow("passkey-stepup:"+string(digest(r.RemoteAddr)), 10) {
		fail(w, 429, errors.New("too many authentication attempts"))
		return nil, false
	}
	var hash string
	var otp sql.NullString
	if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT password_hash,totp_secret FROM users WHERE id=?`, i.UserID).Scan(&hash, &otp); err != nil || !verifyPassword(hash, password) || (otp.Valid && otp.String != "" && !totp.Validate(code, otp.String)) {
		fail(w, 401, errors.New("invalid credentials"))
		return nil, false
	}
	return digest(hash + "\x00" + otp.String), true
}
func passkeyCookie(kind string) string { return "postbox_passkey_" + kind }
func (a *API) savePasskeyCeremony(w http.ResponseWriter, r *http.Request, kind string, user int64, auth []byte, name string, state *webauthn.SessionData) error {
	token := secret()
	expires := time.Now().Add(5 * time.Minute)
	state.Expires = expires
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	var sessionHash []byte
	var userID any
	if user != 0 {
		c, err := r.Cookie("postbox_session")
		if err != nil {
			return err
		}
		sessionHash = digest(c.Value)
		userID = user
	}
	err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		if _, err := c.ExecContext(r.Context(), `DELETE FROM passkey_ceremonies WHERE expires_at<=?`, time.Now().UnixMilli()); err != nil {
			return err
		}
		if previous, err := r.Cookie(passkeyCookie(kind)); err == nil {
			if _, err = c.ExecContext(r.Context(), `DELETE FROM passkey_ceremonies WHERE binding_hash=?`, digest(previous.Value)); err != nil {
				return err
			}
		}
		_, err := c.ExecContext(r.Context(), `INSERT INTO passkey_ceremonies(binding_hash,kind,user_id,session_hash,auth_hash,name,state,expires_at) VALUES(?,?,?,?,?,?,?,?)`, digest(token), kind, userID, sessionHash, auth, name, string(raw), expires.UnixMilli())
		return err
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie(kind), Value: token, Path: "/api/v1/auth/passkeys", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: 300})
	return nil
}

type passkeyCeremony struct {
	user          int64
	session, auth []byte
	name          string
	state         webauthn.SessionData
}

// Consume before parsing/verification, including failed ceremonies. Parallel finishes cannot reuse a challenge.
func (a *API) consumePasskeyCeremony(w http.ResponseWriter, r *http.Request, kind string) (*passkeyCeremony, error) {
	cookie, err := r.Cookie(passkeyCookie(kind))
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie(kind), Value: "", Path: "/api/v1/auth/passkeys", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	s := &passkeyCeremony{}
	var raw string
	err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var uid sql.NullInt64
		var expires int64
		var storedKind string
		err := c.QueryRowContext(r.Context(), `SELECT kind,user_id,session_hash,auth_hash,name,state,expires_at FROM passkey_ceremonies WHERE binding_hash=?`, digest(cookie.Value)).Scan(&storedKind, &uid, &s.session, &s.auth, &s.name, &raw, &expires)
		if err != nil {
			return err
		}
		s.user = uid.Int64
		if _, err = c.ExecContext(r.Context(), `DELETE FROM passkey_ceremonies WHERE binding_hash=?`, digest(cookie.Value)); err != nil {
			return err
		}
		if storedKind != kind || expires <= time.Now().UnixMilli() {
			raw = ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, errors.New("expired ceremony")
	}
	if kind == "register" {
		i := r.Context().Value(identityKey{}).(*identity)
		session, err := r.Cookie("postbox_session")
		if err != nil || i.UserID != s.user || !equal(s.session, digest(session.Value)) {
			return nil, errors.New("invalid ceremony binding")
		}
	}
	if err = json.Unmarshal([]byte(raw), &s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func (a *API) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !a.passkeyAvailable(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
		Name     string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	auth, ok := a.passkeyStepUp(w, r, in.Password, in.Code)
	if !ok {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = "Passkey"
	}
	if len(in.Name) > 100 {
		fail(w, 400, errors.New("passkey name must be at most 100 bytes"))
		return
	}
	id := r.Context().Value(identityKey{}).(*identity).UserID
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		handle := make([]byte, 32)
		if _, err := rand.Read(handle); err != nil {
			return err
		}
		_, err := c.ExecContext(r.Context(), `INSERT INTO passkey_users(user_id,handle) VALUES(?,?) ON CONFLICT(user_id) DO NOTHING`, id, handle)
		return err
	})
	if err != nil {
		fail(w, 500, errors.New("cannot begin passkey enrollment"))
		return
	}
	u, err := a.loadPasskeyUser(r.Context(), a.db.Reader, id)
	if err != nil {
		fail(w, 500, errors.New("cannot begin passkey enrollment"))
		return
	}
	excludes := make([]protocol.CredentialDescriptor, 0, len(u.credentials))
	for _, c := range u.credentials {
		excludes = append(excludes, c.Descriptor())
	}
	options, state, err := a.passkeys.BeginRegistration(u, webauthn.WithExclusions(excludes))
	if err == nil {
		err = a.savePasskeyCeremony(w, r, "register", id, auth, in.Name, state)
	}
	if err != nil {
		fail(w, 500, errors.New("cannot begin passkey enrollment"))
		return
	}
	respond(w, 200, options)
}
func (a *API) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !a.passkeyAvailable(w, r) {
		return
	}
	s, err := a.consumePasskeyCeremony(w, r, "register")
	if err != nil {
		fail(w, 401, errors.New("invalid or expired passkey ceremony"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var id int64
	err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var active int
		if err := c.QueryRowContext(r.Context(), `SELECT count(*) FROM sessions WHERE token_hash=? AND user_id=? AND expires_at>?`, s.session, s.user, time.Now().UnixMilli()).Scan(&active); err != nil {
			return err
		}
		if active != 1 {
			return errors.New("session expired")
		}
		var hash string
		var otp sql.NullString
		if err := c.QueryRowContext(r.Context(), `SELECT password_hash,totp_secret FROM users WHERE id=?`, s.user).Scan(&hash, &otp); err != nil {
			return err
		}
		if !equal(s.auth, digest(hash+"\x00"+otp.String)) {
			return errors.New("authentication changed")
		}
		u, err := a.loadPasskeyUser(r.Context(), c, s.user)
		if err != nil {
			return err
		}
		credential, err := a.passkeys.FinishRegistration(u, s.state, r)
		if err != nil {
			return err
		}
		if !credential.Flags.UserVerified {
			return errors.New("user verification required")
		}
		raw, err := json.Marshal(credential)
		if err != nil {
			return err
		}
		res, err := c.ExecContext(r.Context(), `INSERT INTO passkeys(user_id,rp_id,credential_id,credential,name,created_at) VALUES(?,?,?,?,?,?)`, s.user, a.passkeys.Config.RPID, credential.ID, string(raw), s.name, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		fail(w, 401, errors.New("invalid passkey registration"))
		return
	}
	respond(w, 200, map[string]any{"id": id, "name": s.name})
}
func (a *API) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !a.passkeyAvailable(w, r) {
		return
	}
	options, state, err := a.passkeys.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err == nil {
		err = a.savePasskeyCeremony(w, r, "login", 0, nil, "", state)
	}
	if err != nil {
		fail(w, 500, errors.New("cannot begin passkey login"))
		return
	}
	respond(w, 200, options)
}
func (a *API) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !a.passkeyAvailable(w, r) {
		return
	}
	s, err := a.consumePasskeyCeremony(w, r, "login")
	if err != nil {
		fail(w, 401, errors.New("invalid credentials"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	token, csrf := secret(), secret()
	expires := time.Now().Add(a.cfg.SessionTTL)
	var id int64
	err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		handler := func(rawID, handle []byte) (webauthn.User, error) {
			var uid int64
			err := c.QueryRowContext(r.Context(), `SELECT p.user_id FROM passkeys p JOIN passkey_users u ON u.user_id=p.user_id WHERE p.rp_id=? AND p.credential_id=? AND u.handle=?`, a.passkeys.Config.RPID, rawID, handle).Scan(&uid)
			if err != nil {
				return nil, err
			}
			return a.loadPasskeyUser(r.Context(), c, uid)
		}
		user, credential, err := a.passkeys.FinishPasskeyLogin(handler, s.state, r)
		if err != nil {
			return err
		}
		if !credential.Flags.UserVerified || credential.Authenticator.CloneWarning {
			return errors.New("invalid authenticator state")
		}
		id = user.(*passkeyUser).id
		raw, err := json.Marshal(credential)
		if err != nil {
			return err
		}
		res, err := c.ExecContext(r.Context(), `UPDATE passkeys SET credential=?,last_used_at=? WHERE user_id=? AND rp_id=? AND credential_id=?`, string(raw), time.Now().UnixMilli(), id, a.passkeys.Config.RPID, credential.ID)
		if err = requireChanged(res, err); err != nil {
			return err
		}
		return insertSession(r.Context(), c, id, token, csrf, expires)
	})
	if err != nil {
		fail(w, 401, errors.New("invalid credentials"))
		return
	}
	a.sessionResponse(w, id, token, csrf, expires)
}
func (a *API) listPasskeys(w http.ResponseWriter, r *http.Request) {
	if !a.passkeyAvailable(w, r) {
		return
	}
	id := r.Context().Value(identityKey{}).(*identity).UserID
	if id == 0 {
		fail(w, 403, errors.New("session authentication required"))
		return
	}
	rows, err := queryRows(r.Context(), a.db.Reader, `SELECT id,name,created_at,last_used_at FROM passkeys WHERE user_id=? AND rp_id=? ORDER BY id`, id, a.passkeys.Config.RPID)
	result(w, rows, err)
}
func (a *API) deletePasskey(w http.ResponseWriter, r *http.Request) {
	if !a.passkeyAvailable(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	auth, ok := a.passkeyStepUp(w, r, in.Password, in.Code)
	if !ok {
		return
	}
	uid := r.Context().Value(identityKey{}).(*identity).UserID
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var hash string
		var otp sql.NullString
		if err := c.QueryRowContext(r.Context(), `SELECT password_hash,totp_secret FROM users WHERE id=?`, uid).Scan(&hash, &otp); err != nil {
			return err
		}
		if !equal(auth, digest(hash+"\x00"+otp.String)) {
			return errors.New("authentication changed")
		}
		res, err := c.ExecContext(r.Context(), `DELETE FROM passkeys WHERE id=? AND user_id=? AND rp_id=?`, number(r.PathValue("id")), uid, a.passkeys.Config.RPID)
		if err = requireChanged(res, err); err != nil {
			return err
		}
		// Revocation ends existing sessions and pending additions, including this browser.
		if _, err = c.ExecContext(r.Context(), `DELETE FROM passkey_ceremonies WHERE user_id=?`, uid); err != nil {
			return err
		}
		_, err = c.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, uid)
		return err
	})
	result(w, map[string]bool{"deleted": err == nil, "logged_out": err == nil}, err)
}
