package store

import (
	"context"
	"database/sql"
)

// DeleteBrand removes owned data that intentionally has no foreign key, queued
// work and relational children in the same transaction as the owner.
func (db *DB) DeleteBrand(ctx context.Context, brandID int64) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		queries := []struct {
			query string
			args  []any
		}{
			{"DELETE FROM opens WHERE campaign_id IN (SELECT id FROM campaigns WHERE brand_id=?)", []any{brandID}},
			{"DELETE FROM clicks WHERE campaign_id IN (SELECT id FROM campaigns WHERE brand_id=?)", []any{brandID}},
			{"DELETE FROM subscriber_activity WHERE brand_id=?", []any{brandID}},
			{`DELETE FROM jobs WHERE json_extract(payload,'$.brand_id')=?
				OR json_extract(payload,'$.body.brand_id')=?
				OR json_extract(payload,'$.webhook_id') IN (SELECT id FROM webhooks WHERE brand_id=?)
				OR json_extract(payload,'$.campaign_id') IN (SELECT id FROM campaigns WHERE brand_id=?)`, []any{brandID, brandID, brandID, brandID}},
		}
		for _, q := range queries {
			if _, err := c.ExecContext(ctx, q.query, q.args...); err != nil {
				return err
			}
		}
		res, err := c.ExecContext(ctx, "DELETE FROM brands WHERE id=?", brandID)
		return deletedOwner(res, err)
	})
}

// DeleteList relies on the database's audience protection and draft cleanup
// triggers, and removes its queued notifications atomically with the list.
func (db *DB) DeleteList(ctx context.Context, brandID, listID int64) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		res, err := c.ExecContext(ctx, "DELETE FROM lists WHERE brand_id=? AND id=?", brandID, listID)
		if err = deletedOwner(res, err); err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, `DELETE FROM jobs WHERE
			(json_extract(payload,'$.list_id')=? AND json_extract(payload,'$.brand_id')=?) OR
			(json_extract(payload,'$.body.data.list_id')=? AND json_extract(payload,'$.body.brand_id')=?)`, listID, brandID, listID, brandID)
		return err
	})
}

// DeleteCampaign atomically removes raw engagement/queued work and relational
// children. The expected status fences concurrent lifecycle changes.
func (db *DB) DeleteCampaign(ctx context.Context, brandID, campaignID int64, expectedStatus string) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		res, err := c.ExecContext(ctx, "DELETE FROM campaigns WHERE brand_id=? AND id=? AND status=? AND status IN ('draft','sent','cancelled')", brandID, campaignID, expectedStatus)
		if err = deletedOwner(res, err); err != nil {
			return err
		}
		for _, query := range []string{
			"DELETE FROM opens WHERE campaign_id=?",
			"DELETE FROM clicks WHERE campaign_id=?",
			`DELETE FROM jobs WHERE ? IN (json_extract(payload,'$.campaign_id'),json_extract(payload,'$.data.campaign_id'),json_extract(payload,'$.body.data.campaign_id'))`,
		} {
			if _, err = c.ExecContext(ctx, query, campaignID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) DeleteSegment(ctx context.Context, brandID, segmentID int64) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		res, err := c.ExecContext(ctx, "DELETE FROM segments WHERE brand_id=? AND id=?", brandID, segmentID)
		return deletedOwner(res, err)
	})
}

func deletedOwner(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
