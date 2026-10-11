package subscribers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"postbox/internal/consent"
	"postbox/internal/jobs"
	"postbox/internal/render"
	"strings"
)

func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Service) CreateBrand(ctx context.Context, v Brand) (Brand, error) {
	email, e := render.NormalizeEmail(v.FromEmail)
	if e != nil {
		return v, e
	}
	v.FromEmail = email
	v.CreatedAt = nowMS()
	r, e := s.db.Writer.ExecContext(ctx, "INSERT INTO brands(name,from_name,from_email,reply_to,configuration_set,tracking_domain,created_at) VALUES(?,?,?,?,?,NULLIF(?,''),?)", v.Name, v.FromName, v.FromEmail, v.ReplyTo, v.ConfigurationSet, v.TrackingDomain, v.CreatedAt)
	if e == nil {
		v.ID, e = r.LastInsertId()
	}
	return v, e
}
func (s *Service) Brands(ctx context.Context) ([]Brand, error) {
	rows, e := s.db.Reader.QueryContext(ctx, "SELECT id,name,from_name,from_email,coalesce(reply_to,''),coalesce(configuration_set,''),coalesce(tracking_domain,''),created_at FROM brands ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Brand{}
	for rows.Next() {
		var v Brand
		if e = rows.Scan(&v.ID, &v.Name, &v.FromName, &v.FromEmail, &v.ReplyTo, &v.ConfigurationSet, &v.TrackingDomain, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) CreateList(ctx context.Context, v List) (List, error) {
	if v.OptIn == "" {
		v.OptIn = "single"
	}
	if v.OptIn != "single" && v.OptIn != "double" {
		return v, errors.New("opt_in must be single or double")
	}
	v.UUID = randomToken()
	v.CreatedAt = nowMS()
	e := s.db.Immediate(ctx, func(c *sql.Conn) error {
		r, e := c.ExecContext(ctx, "INSERT INTO lists(brand_id,uuid,name,opt_in,created_at) VALUES(?,?,?,?,?)", v.BrandID, v.UUID, v.Name, v.OptIn, v.CreatedAt)
		if e != nil {
			return e
		}
		v.ID, e = r.LastInsertId()
		if e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, "INSERT INTO list_counters(list_id) VALUES(?)", v.ID)
		return e
	})
	return v, e
}
func (s *Service) Lists(ctx context.Context, brandID int64) ([]List, error) {
	rows, e := s.db.Reader.QueryContext(ctx, "SELECT id,brand_id,uuid,name,opt_in,created_at FROM lists WHERE brand_id=? ORDER BY id", brandID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []List{}
	for rows.Next() {
		var v List
		if e = rows.Scan(&v.ID, &v.BrandID, &v.UUID, &v.Name, &v.OptIn, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) UpdateList(ctx context.Context, v List) error {
	if v.OptIn != "single" && v.OptIn != "double" {
		return errors.New("invalid opt_in")
	}
	_, e := s.db.Writer.ExecContext(ctx, "UPDATE lists SET name=?,opt_in=? WHERE brand_id=? AND id=?", v.Name, v.OptIn, v.BrandID, v.ID)
	return e
}
func (s *Service) DeleteList(ctx context.Context, brandID, id int64) error {
	_, e := s.db.Writer.ExecContext(ctx, "DELETE FROM lists WHERE brand_id=? AND id=?", brandID, id)
	return e
}

const subscriberColumns = "id,brand_id,email,name,fields,status,gdpr_consent_at,coalesce(gdpr_consent_ip,''),created_at,updated_at,marketing_unsubscribed_at"

func scanSubscriber(row interface{ Scan(...any) error }) (Subscriber, error) {
	var v Subscriber
	var fields string
	e := row.Scan(&v.ID, &v.BrandID, &v.Email, &v.Name, &fields, &v.Status, &v.ConsentAt, &v.ConsentIP, &v.CreatedAt, &v.UpdatedAt, &v.MarketingUnsubscribedAt)
	if e == nil {
		e = json.Unmarshal([]byte(fields), &v.Fields)
	}
	return v, e
}
func (s *Service) GetByEmail(ctx context.Context, brandID int64, email string) (Subscriber, error) {
	n, e := render.NormalizeEmail(email)
	if e != nil {
		return Subscriber{}, e
	}
	return scanSubscriber(s.db.Reader.QueryRowContext(ctx, "SELECT "+subscriberColumns+" FROM subscribers WHERE brand_id=? AND email=?", brandID, n))
}
func (s *Service) GetByID(ctx context.Context, brandID, id int64) (Subscriber, error) {
	return scanSubscriber(s.db.Reader.QueryRowContext(ctx, "SELECT "+subscriberColumns+" FROM subscribers WHERE brand_id=? AND id=?", brandID, id))
}
func (s *Service) Search(ctx context.Context, brandID, afterID int64, limit int) ([]Subscriber, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, e := s.db.Reader.QueryContext(ctx, "SELECT "+subscriberColumns+" FROM subscribers WHERE brand_id=? AND id>? ORDER BY id LIMIT ?", brandID, afterID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Subscriber{}
	for rows.Next() {
		v, e := scanSubscriber(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Upsert(ctx context.Context, in UpsertInput) (Subscriber, error) {
	var v Subscriber
	e := s.db.Immediate(ctx, func(c *sql.Conn) error { var e error; v, e = s.upsert(ctx, c, in); return e })
	return v, e
}
func (s *Service) upsert(ctx context.Context, c *sql.Conn, in UpsertInput) (Subscriber, error) {
	return s.upsertPrepared(ctx, c, in, nil, nil)
}

const subscriberUpsertSQL = "INSERT INTO subscribers(brand_id,email,name,fields,status,gdpr_consent_at,gdpr_consent_ip,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(brand_id,email) DO UPDATE SET name=excluded.name,fields=json_patch(subscribers.fields,excluded.fields),status=CASE WHEN subscribers.status='active' THEN excluded.status ELSE subscribers.status END,gdpr_consent_at=coalesce(subscribers.gdpr_consent_at,excluded.gdpr_consent_at),gdpr_consent_ip=CASE WHEN subscribers.gdpr_consent_at IS NULL THEN excluded.gdpr_consent_ip ELSE subscribers.gdpr_consent_ip END,updated_at=excluded.updated_at RETURNING " + subscriberColumns

func (s *Service) upsertPrepared(ctx context.Context, c *sql.Conn, in UpsertInput, prepared *sql.Stmt, memberships *membershipBatch) (Subscriber, error) {
	if in.SubscriptionStatus != "" && in.SubscriptionStatus != "subscribed" && in.SubscriptionStatus != "unsubscribed" && in.SubscriptionStatus != "unconfirmed" {
		return Subscriber{}, errors.New("invalid subscription_status")
	}
	email, e := render.NormalizeEmail(in.Email)
	if e != nil {
		return Subscriber{}, e
	}
	if in.BrandID <= 0 {
		return Subscriber{}, errors.New("brand_id is required")
	}
	status := in.Status
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "bounced" && status != "complained" && status != "blocklisted" {
		return Subscriber{}, errors.New("invalid status")
	}
	if in.Fields == nil {
		in.Fields = map[string]any{}
	}
	fields, e := json.Marshal(in.Fields)
	if e != nil {
		return Subscriber{}, e
	}
	at := nowMS()
	var consent any
	if in.Consent {
		consent = at
	}
	if in.ConsentAt != nil {
		consent = *in.ConsentAt
	}
	// Existing suppressed status must never be revived by a profile upsert.
	var row *sql.Row
	if prepared != nil {
		row = prepared.QueryRowContext(ctx, in.BrandID, email, in.Name, string(fields), status, consent, in.ConsentIP, at, at)
	} else {
		row = c.QueryRowContext(ctx, subscriberUpsertSQL, in.BrandID, email, in.Name, string(fields), status, consent, in.ConsentIP, at, at)
	}
	v, e := scanSubscriber(row)
	if e != nil {
		return v, e
	}
	return s.applyMemberships(ctx, c, in, v, at, memberships)
}

func (s *Service) Unsubscribe(ctx context.Context, brandID, id, listID int64) error {
	return s.db.Immediate(ctx, func(c *sql.Conn) error { return unsubscribe(ctx, c, brandID, id, listID) })
}
func unsubscribe(ctx context.Context, c *sql.Conn, brandID, id, listID int64) error {
	return unsubscribeMemberships(ctx, c, brandID, id, listID, true, true)
}
func unsubscribeMemberships(ctx context.Context, c *sql.Conn, brandID, id, listID int64, recordOptOut, emitEvents bool) error {
	var found int
	if e := c.QueryRowContext(ctx, `SELECT 1 FROM subscribers WHERE id=? AND brand_id=?`, id, brandID).Scan(&found); e != nil {
		return e
	}
	if listID == 0 && recordOptOut {
		if e := consent.RecordBrandOptOutTx(ctx, c, brandID, id, nowMS()); e != nil {
			return e
		}
	}

	ids, e := consent.UnsubscribeMembershipsTx(ctx, c, id, listID, nowMS(), "unsubscribe")
	if e != nil {
		return e
	}
	if len(ids) == 0 && listID > 0 {
		return nil
	}
	if emitEvents {
		for _, list := range ids {
			if e = jobs.EmitTx(ctx, c, brandID, "unsubscribe", map[string]any{"subscriber_id": id, "list_id": list}); e != nil {
				return e
			}
		}
	}
	if e = SkipAfterUnsubscribeTx(ctx, c, id, listID); e != nil {
		return e
	}
	kind := "unsubscribe"
	if !recordOptOut {
		kind = "suppressed"
	}
	_, e = c.ExecContext(ctx, "INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) SELECT brand_id,id,?,?,json_object('list_id',?) FROM subscribers WHERE id=? AND brand_id=?", kind, nowMS(), listID, id, brandID)
	return e
}
func (s *Service) AddSuppression(ctx context.Context, v Suppression) error {
	return s.AddSuppressionChecked(ctx, v, nil)
}

// AddSuppressionChecked validates a caller's snapshot within the same write
// transaction that imports the suppression and skips pending recipients.
func (s *Service) AddSuppressionChecked(ctx context.Context, v Suppression, check func(*sql.Conn) error) error {
	var e error
	v, e = normalizeSuppression(v)
	if e != nil {
		return e
	}
	if v.Reason == "" {
		v.Reason = "manual"
	}
	return s.db.Immediate(ctx, func(c *sql.Conn) error {
		if check != nil {
			if err := check(c); err != nil {
				return err
			}
		}

		if _, e := c.ExecContext(ctx, "INSERT OR IGNORE INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(?,?,?,?,?)", v.BrandID, v.Kind, v.Value, v.Reason, nowMS()); e != nil {
			return e
		}
		column := "email"
		if v.Kind == "domain" {
			column = "domain"
		}
		return skipPendingTx(ctx, c, "subscriber_id IN (SELECT id FROM subscribers WHERE "+column+"=? AND (? IS NULL OR brand_id=?))", v.Value, v.BrandID, v.BrandID)
	})
}

// SuppressionPatch preserves fields omitted by the caller.
type SuppressionPatch struct {
	Kind   *string `json:"kind"`
	Value  *string `json:"value"`
	Reason *string `json:"reason"`
}

var ErrSuppressionConflict = errors.New("a suppression with this kind and value already exists in this scope")

func normalizeSuppression(v Suppression) (Suppression, error) {
	value := strings.TrimSpace(v.Value)
	switch v.Kind {
	case "email":
		n, err := render.NormalizeEmail(value)
		if err != nil {
			return v, err
		}
		_, domain, _ := strings.Cut(n, "@")
		if !strings.Contains(domain, ".") {
			return v, errors.New("email must contain a full domain, such as person@example.com")
		}
		v.Value = n
	case "domain":
		if strings.Contains(value, "@") {
			return v, errors.New("domain must not contain an email address")
		}
		n, err := render.NormalizeEmail("validation@" + value)
		if err != nil {
			return v, err
		}
		_, v.Value, _ = strings.Cut(n, "@")
	default:
		return v, errors.New("kind must be email or domain")
	}
	return v, nil
}

// UpdateSuppression applies a patch within an immutable brand or global scope.
func (s *Service) UpdateSuppression(ctx context.Context, id int64, brandID *int64, patch SuppressionPatch) (Suppression, error) {
	var v Suppression
	err := s.db.Immediate(ctx, func(c *sql.Conn) error {
		v.ID, v.BrandID = id, brandID
		if err := c.QueryRowContext(ctx, "SELECT kind,value,reason FROM suppressions WHERE id=? AND brand_id IS ?", id, brandID).Scan(&v.Kind, &v.Value, &v.Reason); err != nil {
			return err
		}
		if patch.Kind != nil {
			v.Kind = *patch.Kind
		}
		if patch.Value != nil {
			v.Value = *patch.Value
		}
		if patch.Reason != nil {
			v.Reason = *patch.Reason
		}
		var err error
		v, err = normalizeSuppression(v)
		if err != nil {
			return err
		}
		switch v.Reason {
		case "hard_bounce", "soft_bounce_limit", "complaint", "manual", "ses_sync":
		default:
			return errors.New("reason must be hard_bounce, soft_bounce_limit, complaint, manual, or ses_sync")
		}
		var duplicate int
		if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM suppressions WHERE kind=? AND value=? AND brand_id IS ? AND id<>?)", v.Kind, v.Value, brandID, id).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			return ErrSuppressionConflict
		}
		if _, err := c.ExecContext(ctx, "UPDATE suppressions SET kind=?,value=?,reason=? WHERE id=? AND brand_id IS ?", v.Kind, v.Value, v.Reason, id, brandID); err != nil {
			return err
		}
		column := "email"
		if v.Kind == "domain" {
			column = "domain"
		}
		return skipPendingTx(ctx, c, "subscriber_id IN (SELECT id FROM subscribers WHERE "+column+"=? AND (? IS NULL OR brand_id=?))", v.Value, brandID, brandID)
	})
	return v, err
}
