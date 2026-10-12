package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
)

func (a *API) changeEmail(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("session authentication required"))
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	addr, err := mail.ParseAddress(in.Email)
	if err != nil || addr.Address != in.Email {
		fail(w, 400, errors.New("valid email required"))
		return
	}
	if !a.allow(a.rateKey(r, "email-stepup", true), 10) {
		fail(w, 429, errors.New("too many authentication attempts"))
		return
	}
	var hash string
	var otp sql.NullString
	if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT password_hash,totp_secret FROM users WHERE id=?`, i.UserID).Scan(&hash, &otp); err != nil || !verifyPassword(hash, in.Password) || (otp.Valid && otp.String != "" && !totp.Validate(in.Code, otp.String)) {
		fail(w, 400, errors.New("current password and, when enabled, a valid authenticator code required"))
		return
	}
	cookie, err := r.Cookie("postbox_session")
	if err != nil {
		fail(w, 403, errors.New("session authentication required"))
		return
	}
	err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var currentHash string
		var currentOTP sql.NullString
		if err := c.QueryRowContext(r.Context(), `SELECT u.password_hash,u.totp_secret FROM users u JOIN sessions s ON s.user_id=u.id WHERE u.id=? AND s.token_hash=? AND s.expires_at>?`, i.UserID, digest(cookie.Value), time.Now().UnixMilli()).Scan(&currentHash, &currentOTP); err != nil {
			return err
		}
		if currentHash != hash || currentOTP != otp {
			return errors.New("authentication changed; try again")
		}
		var existing int64
		if err := c.QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE email=? AND id<>?`, in.Email, i.UserID).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return errors.New("email address already in use")
		}
		if _, err := c.ExecContext(r.Context(), `UPDATE users SET email=? WHERE id=?`, in.Email, i.UserID); err != nil {
			return err
		}
		if _, err := c.ExecContext(r.Context(), `DELETE FROM passkey_ceremonies WHERE user_id=?`, i.UserID); err != nil {
			return err
		}
		_, err := c.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, i.UserID, digest(cookie.Value))
		return err
	})
	w.Header().Set("Cache-Control", "no-store")
	result(w, map[string]string{"email": in.Email}, err)
}
