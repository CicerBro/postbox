package subscribers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"postbox/internal/render"
)

// PublicSubscribe accepts anonymous opt-in requests. Only creation may set profile
// data; an existing address keeps its profile until an authenticated edit.
func (s *Service) PublicSubscribe(ctx context.Context, in UpsertInput) (Subscriber, error) {
	email, err := render.NormalizeEmail(in.Email)
	if err != nil {
		return Subscriber{}, err
	}
	if in.BrandID <= 0 || len(in.ListIDs) == 0 {
		return Subscriber{}, errors.New("brand and list are required")
	}
	in.Email = email
	in.Status = "active"
	in.SubscriptionStatus = ""
	in.ConsentAt = nil
	in.SuppressNotifications = false
	var v Subscriber
	err = s.db.Immediate(ctx, func(c *sql.Conn) error {
		at := nowMS()
		var e error
		v, e = s.findOnConn(ctx, c, in.BrandID, email)
		if e == sql.ErrNoRows {
			fields, e := json.Marshal(in.Fields)
			if e != nil {
				return e
			}
			if in.Fields == nil {
				fields = []byte("{}")
			}
			var consent any
			if in.Consent {
				consent = at
			}
			v, e = scanSubscriber(c.QueryRowContext(ctx, "INSERT INTO subscribers(brand_id,email,name,fields,status,gdpr_consent_at,gdpr_consent_ip,created_at,updated_at) VALUES(?,?,?,?,'active',?,?,?,?) RETURNING "+subscriberColumns, in.BrandID, email, in.Name, string(fields), consent, in.ConsentIP, at, at))
			if e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else if in.Consent && v.ConsentAt == nil {
			if _, e = c.ExecContext(ctx, "UPDATE subscribers SET gdpr_consent_at=?,gdpr_consent_ip=? WHERE id=? AND gdpr_consent_at IS NULL", at, in.ConsentIP, v.ID); e != nil {
				return e
			}
			v.ConsentAt = &at
			v.ConsentIP = in.ConsentIP
		}
		v, e = s.applyMemberships(ctx, c, in, v, at, nil)
		return e
	})
	return v, err
}
