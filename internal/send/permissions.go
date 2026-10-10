package send

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"postbox/internal/store"
	"time"
)

// ReserveUserCampaign charges a conservative recipient budget before any SES call.
// It is idempotent across campaign restarts. Unknown outcomes remain charged.
func ReserveUserCampaign(ctx context.Context, db *store.DB, campaignID, recipients int64) error {
	if recipients < 0 {
		return errors.New("invalid recipient count")
	}
	return reserveUser(ctx, db, campaignID, recipients, false)
}

// ReserveUserTestSend charges each preview delivery attempt separately.
func ReserveUserTestSend(ctx context.Context, db *store.DB, campaignID, recipients int64) error {
	if recipients < 0 {
		return errors.New("invalid recipient count")
	}
	return reserveUser(ctx, db, campaignID, recipients, true)
}

func reserveUser(ctx context.Context, db *store.DB, campaignID, recipients int64, test bool) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		var user sql.NullInt64
		var brand int64
		if err := c.QueryRowContext(ctx, `SELECT created_by,brand_id FROM campaigns WHERE id=?`, campaignID).Scan(&user, &brand); err != nil {
			return err
		}
		if !user.Valid {
			return nil
		}
		var role string
		if err := c.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, user.Int64).Scan(&role); err != nil {
			return err
		}
		if role == "admin" {
			return nil
		}
		var perms string
		var limit sql.NullInt64
		if err := c.QueryRowContext(ctx, `SELECT permissions,daily_send_limit FROM user_brands WHERE user_id=? AND brand_id=?`, user.Int64, brand).Scan(&perms, &limit); err != nil {
			return errors.New("campaign owner no longer has brand access")
		}
		var scopes []string
		if err := json.Unmarshal([]byte(perms), &scopes); err != nil {
			return err
		}
		allowed := false
		for _, s := range scopes {
			if s == "send" {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("campaign owner lacks send permission")
		}
		var previous int64
		err := sql.ErrNoRows
		if !test {
			err = c.QueryRowContext(ctx, `SELECT recipients FROM campaign_user_reservations WHERE campaign_id=?`, campaignID).Scan(&previous)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && recipients <= previous {
			return nil
		}
		total := recipients
		recipients -= previous
		day := time.Now().UTC().Truncate(24 * time.Hour).UnixMilli()
		var used int64
		if err := c.QueryRowContext(ctx, `SELECT coalesce((SELECT reserved FROM user_brand_daily WHERE user_id=? AND brand_id=? AND day=?),0)`, user.Int64, brand, day).Scan(&used); err != nil {
			return err
		}
		if limit.Valid && (used > limit.Int64 || recipients > limit.Int64-used) {
			return errors.New("campaign exceeds user's remaining daily sending limit")
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO user_brand_daily(user_id,brand_id,day,reserved) VALUES(?,?,?,?) ON CONFLICT(user_id,brand_id,day) DO UPDATE SET reserved=reserved+excluded.reserved`, user.Int64, brand, day, recipients); err != nil {
			return err
		}
		if test {
			return nil
		}
		_, err = c.ExecContext(ctx, `INSERT INTO campaign_user_reservations(campaign_id,user_id,brand_id,day,recipients) VALUES(?,?,?,?,?) ON CONFLICT(campaign_id) DO UPDATE SET recipients=excluded.recipients`, campaignID, user.Int64, brand, day, total)
		return err
	})
}
