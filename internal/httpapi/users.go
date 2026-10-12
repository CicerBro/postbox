package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/pquerna/otp/totp"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

func (a *API) registerUsers(m *http.ServeMux) {
	for _, p := range []string{"GET /api/v1/users", "POST /api/v1/users", "GET /api/v1/users/{id}", "PATCH /api/v1/users/{id}", "DELETE /api/v1/users/{id}"} {
		a.route(m, p, a.users)
	}
	for _, p := range []string{"GET /api/v1/users/{id}/brands", "PUT /api/v1/users/{id}/brands/{brand}", "DELETE /api/v1/users/{id}/brands/{brand}"} {
		a.route(m, p, a.userBrands)
	}
	a.route(m, "POST /api/v1/auth/totp/enroll", a.enrollTOTP)
	a.route(m, "POST /api/v1/auth/totp/verify", a.verifyTOTP)
	a.route(m, "DELETE /api/v1/auth/totp", a.deleteTOTP)
	a.route(m, "DELETE /api/v1/users/{id}/totp", a.resetTOTP)
	a.route(m, "PATCH /api/v1/auth/email", a.changeEmail)
	a.documentUsers()
}
func (a *API) userAdmin(w http.ResponseWriter, r *http.Request) bool {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 || !i.Admin {
		fail(w, 403, errors.New("administrator session required"))
		return false
	}
	return true
}
func (a *API) users(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	id := number(r.PathValue("id"))
	if r.Method == "GET" {
		if id > 0 {
			rows, err := queryRows(r.Context(), a.db.Reader, `SELECT id,email,display_name,role,created_at,totp_secret IS NOT NULL AS totp_enabled FROM users WHERE id=?`, id)
			if err == nil && len(rows) == 0 {
				err = sql.ErrNoRows
			}
			if len(rows) > 0 {
				result(w, rows[0], err)
			} else {
				result(w, nil, err)
			}
			return
		}
		after, limit := page(r)
		rows, err := queryRows(r.Context(), a.db.Reader, `SELECT id,email,display_name,role,created_at,totp_secret IS NOT NULL AS totp_enabled FROM users WHERE id>? ORDER BY id LIMIT ?`, after, limit)
		result(w, rows, err)
		return
	}
	if r.Method == "DELETE" {
		err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
			if err := protectAdmin(r, c, id, ""); err != nil {
				return err
			}
			if _, err := c.ExecContext(r.Context(), `UPDATE campaigns SET status='cancelled',updated_at=? WHERE created_by=? AND status NOT IN ('sent','cancelled')`, time.Now().UnixMilli(), id); err != nil {
				return err
			}
			res, err := c.ExecContext(r.Context(), `DELETE FROM users WHERE id=?`, id)
			return requireChanged(res, err)
		})
		result(w, map[string]bool{"deleted": err == nil}, err)
		return
	}
	var in struct {
		Email       string  `json:"email"`
		DisplayName *string `json:"display_name"`
		Password    string  `json:"password"`
		Role        string  `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.DisplayName != nil {
		name, err := normalizeDisplayName(*in.DisplayName)
		if err != nil {
			fail(w, 400, err)
			return
		}
		in.DisplayName = &name
	}
	if r.Method == "POST" && in.Role == "" {
		in.Role = "brand_user"
	}
	if in.Role != "" && in.Role != "admin" && in.Role != "brand_user" {
		fail(w, 400, errors.New("role must be admin or brand_user"))
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Email != "" {
		addr, err := mail.ParseAddress(in.Email)
		if err != nil || addr.Address != in.Email {
			fail(w, 400, errors.New("valid email required"))
			return
		}
	}
	if r.Method == "POST" && (in.Email == "" || in.Password == "") {
		fail(w, 400, errors.New("email and password required"))
		return
	}
	hash := ""
	if in.Password != "" {
		var err error
		hash, err = HashPassword(in.Password)
		if err != nil {
			fail(w, 400, err)
			return
		}
	}
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		if r.Method == "POST" {
			res, err := c.ExecContext(r.Context(), `INSERT INTO users(email,password_hash,role,created_at,display_name) VALUES(?,?,?,?,COALESCE(?,''))`, in.Email, hash, in.Role, time.Now().UnixMilli(), in.DisplayName)
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			return err
		}
		if in.Role != "" {
			if err := protectAdmin(r, c, id, in.Role); err != nil {
				return err
			}
		}
		res, err := c.ExecContext(r.Context(), `UPDATE users SET display_name=COALESCE(?,display_name),email=CASE WHEN ?='' THEN email ELSE ? END,password_hash=CASE WHEN ?='' THEN password_hash ELSE ? END,role=CASE WHEN ?='' THEN role ELSE ? END WHERE id=?`, in.DisplayName, in.Email, in.Email, hash, hash, in.Role, in.Role, id)
		if err = requireChanged(res, err); err != nil {
			return err
		}
		_, err = c.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, id)
		return err
	})
	result(w, map[string]any{"id": id}, err)
}
func protectAdmin(r *http.Request, c *sql.Conn, id int64, newRole string) error {
	var role string
	if err := c.QueryRowContext(r.Context(), `SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
		return err
	}
	if role == "admin" && newRole != "admin" {
		var count int
		if err := c.QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE role='admin'`).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return errors.New("cannot remove the last administrator")
		}
	}
	return nil
}
func (a *API) userBrands(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	id := number(r.PathValue("id"))
	brand := number(r.PathValue("brand"))
	if r.Method == "GET" {
		after, limit := page(r)
		rows, err := queryRows(r.Context(), a.db.Reader, `SELECT brand_id,permissions,daily_send_limit FROM user_brands WHERE user_id=? AND brand_id>? ORDER BY brand_id LIMIT ?`, id, after, limit)
		result(w, rows, err)
		return
	}
	if id <= 0 || brand <= 0 {
		fail(w, 400, errors.New("valid user and brand required"))
		return
	}
	if r.Method == "DELETE" {
		res, err := a.db.Writer.ExecContext(r.Context(), `DELETE FROM user_brands WHERE user_id=? AND brand_id=?`, id, brand)
		result(w, map[string]bool{"deleted": err == nil}, requireChanged(res, err))
		return
	}
	var in struct {
		Permissions    []string `json:"permissions"`
		DailySendLimit *int64   `json:"daily_send_limit"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.DailySendLimit != nil && *in.DailySendLimit < 0 {
		fail(w, 400, errors.New("daily_send_limit must be nonnegative or null"))
		return
	}
	if len(in.Permissions) == 0 {
		fail(w, 400, errors.New("at least one permission required"))
		return
	}
	seen := map[string]bool{}
	for _, p := range in.Permissions {
		if (p != "read" && p != "write" && p != "send") || seen[p] {
			fail(w, 400, errors.New("permissions must be unique read, write, or send values"))
			return
		}
		seen[p] = true
	}
	perms, _ := json.Marshal(in.Permissions)
	_, err := a.db.Writer.ExecContext(r.Context(), `INSERT INTO user_brands(user_id,brand_id,permissions,daily_send_limit) VALUES(?,?,?,?) ON CONFLICT(user_id,brand_id) DO UPDATE SET permissions=excluded.permissions,daily_send_limit=excluded.daily_send_limit`, id, brand, string(perms), in.DailySendLimit)
	result(w, map[string]any{"user_id": id, "brand_id": brand, "permissions": in.Permissions, "daily_send_limit": in.DailySendLimit}, err)
}
func (a *API) passwordConfirmation(w http.ResponseWriter, r *http.Request, password string) bool {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("session authentication required"))
		return false
	}
	if !a.allow(a.rateKey(r, "mfa", true), 10) {
		fail(w, 429, errors.New("too many MFA attempts"))
		return false
	}
	var hash string
	if a.db.Reader.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id=?`, i.UserID).Scan(&hash) != nil || !verifyPassword(hash, password) {
		fail(w, 401, errors.New("current password required"))
		return false
	}
	return true
}
func (a *API) enrollTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) || !a.passwordConfirmation(w, r, in.Password) {
		return
	}
	i := r.Context().Value(identityKey{}).(*identity)
	var email string
	var enabled bool
	if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT email,totp_secret IS NOT NULL FROM users WHERE id=?`, i.UserID).Scan(&email, &enabled); err != nil {
		result(w, nil, err)
		return
	}
	if enabled {
		fail(w, 409, errors.New("TOTP already enabled; remove it before enrollment"))
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Postbox", AccountName: email})
	if err != nil {
		result(w, nil, err)
		return
	}
	cookie, _ := r.Cookie("postbox_session")
	_, err = a.db.Writer.ExecContext(r.Context(), `INSERT INTO totp_pending(token_hash,secret,expires_at) VALUES(?,?,?) ON CONFLICT(token_hash) DO UPDATE SET secret=excluded.secret,expires_at=excluded.expires_at`, digest(cookie.Value), key.Secret(), time.Now().Add(5*time.Minute).UnixMilli())
	result(w, map[string]any{"secret": key.Secret(), "url": key.URL(), "expires_in": 300}, err)
}
func (a *API) verifyTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("session authentication required"))
		return
	}
	if !a.allow(a.rateKey(r, "totp-verify", true), 10) {
		fail(w, 429, errors.New("too many MFA attempts"))
		return
	}
	cookie, _ := r.Cookie("postbox_session")
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var value string
		if err := c.QueryRowContext(r.Context(), `SELECT secret FROM totp_pending WHERE token_hash=? AND expires_at>?`, digest(cookie.Value), time.Now().UnixMilli()).Scan(&value); err != nil {
			return errors.New("TOTP enrollment expired or missing")
		}
		if !totp.Validate(in.Code, value) {
			return errors.New("invalid TOTP code")
		}
		res, err := c.ExecContext(r.Context(), `UPDATE users SET totp_secret=? WHERE id=? AND totp_secret IS NULL`, value, i.UserID)
		if err = requireChanged(res, err); err != nil {
			return err
		}
		if _, err = c.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, i.UserID, digest(cookie.Value)); err != nil {
			return err
		}
		_, err = c.ExecContext(r.Context(), `DELETE FROM totp_pending WHERE token_hash=?`, digest(cookie.Value))
		return err
	})
	result(w, map[string]bool{"totp_enabled": err == nil}, err)
}
func (a *API) deleteTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) || !a.passwordConfirmation(w, r, in.Password) {
		return
	}
	i := r.Context().Value(identityKey{}).(*identity)
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var value sql.NullString
		if err := c.QueryRowContext(r.Context(), `SELECT totp_secret FROM users WHERE id=?`, i.UserID).Scan(&value); err != nil {
			return err
		}
		if value.Valid && !totp.Validate(in.Code, value.String) {
			return errors.New("current TOTP code required")
		}
		if _, err := c.ExecContext(r.Context(), `UPDATE users SET totp_secret=NULL WHERE id=?`, i.UserID); err != nil {
			return err
		}
		_, err := c.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, i.UserID)
		return err
	})
	result(w, map[string]bool{"totp_enabled": false}, err)
}
func (a *API) resetTOTP(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) || !a.passwordConfirmation(w, r, in.Password) {
		return
	}
	id := number(r.PathValue("id"))
	i := r.Context().Value(identityKey{}).(*identity)
	if id == i.UserID {
		fail(w, 403, errors.New("use authenticated TOTP removal for your own account"))
		return
	}
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		res, err := c.ExecContext(r.Context(), `UPDATE users SET totp_secret=NULL WHERE id=?`, id)
		if err = requireChanged(res, err); err != nil {
			return err
		}
		_, err = c.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, id)
		return err
	})
	result(w, map[string]bool{"totp_enabled": false}, err)
}

func (a *API) documentUsers() {
	for path, v := range a.paths {
		if !strings.HasPrefix(path, "/api/v1/users") && !strings.Contains(path, "/auth/totp") {
			continue
		}
		for method, value := range v.(map[string]any) {
			op := value.(map[string]any)
			op["security"] = []map[string]any{{"cookieAuth": []string{}}}
			op["description"] = "User management requires an administrator session. MFA enrollment requires the current password and a separate code verification within five minutes; deletion requires password and current code. Admin resets require the acting administrator password."
			props := map[string]any{}
			required := []string{}
			switch {
			case strings.HasSuffix(path, "/brands/{brand}") && method == "put":
				props["permissions"] = map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"read", "write", "send"}}}
				props["daily_send_limit"] = map[string]any{"type": "integer", "nullable": true, "minimum": 0, "description": "UTC daily recipient reservation limit. Null means unlimited. Unknown outcomes remain charged."}
				required = []string{"permissions"}
			case strings.HasSuffix(path, "/enroll"):
				props["password"] = map[string]any{"type": "string"}
				required = []string{"password"}
			case strings.HasSuffix(path, "/verify"):
				props["code"] = map[string]any{"type": "string"}
				required = []string{"code"}
			case strings.HasSuffix(path, "/totp"):
				props["password"] = map[string]any{"type": "string"}
				required = []string{"password"}
				if path == "/api/v1/auth/totp" {
					props["code"] = map[string]any{"type": "string"}
					required = append(required, "code")
				}
			case path == "/api/v1/users" || path == "/api/v1/users/{id}":
				props["display_name"] = displayNameSchema()
				props["email"] = map[string]any{"type": "string", "format": "email"}
				props["password"] = map[string]any{"type": "string", "minLength": 8}
				props["role"] = map[string]any{"type": "string", "enum": []string{"admin", "brand_user"}}
				if method == "post" {
					required = []string{"email", "password"}
				}
			}
			if len(props) > 0 && method != "get" && (method != "delete" || strings.HasSuffix(path, "/totp")) {
				op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}}}
			}
		}
	}
}

func userRequestScope(r *http.Request) string {
	if r.Method == "GET" || r.Method == "HEAD" {
		return "read"
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/campaigns/") {
		for _, action := range []string{"start", "schedule", "resume", "test-send", "resend-unknown"} {
			if has(r.URL.Path, action) {
				return "send"
			}
		}
	}
	return "write"
}
