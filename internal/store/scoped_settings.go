package store

import (
	"context"
	"database/sql"
)

func (db *DB) BrandSetting(ctx context.Context, brandID int64, name string) (string, error) {
	var value string
	err := db.Reader.QueryRowContext(ctx, "SELECT value FROM brand_settings WHERE brand_id=? AND name=?", brandID, name).Scan(&value)
	return value, err
}

func (db *DB) ListSetting(ctx context.Context, listID int64, name string) (string, error) {
	var value string
	err := db.Reader.QueryRowContext(ctx, "SELECT value FROM list_settings WHERE list_id=? AND name=?", listID, name).Scan(&value)
	return value, err
}

func (db *DB) BrandSettings(ctx context.Context, brandID int64) (map[string]string, error) {
	return scopedSettings(ctx, db.Reader, "SELECT name,value FROM brand_settings WHERE brand_id=?", brandID)
}

func (db *DB) ListSettings(ctx context.Context, listID int64) (map[string]string, error) {
	return scopedSettings(ctx, db.Reader, "SELECT name,value FROM list_settings WHERE list_id=?", listID)
}

func scopedSettings(ctx context.Context, q *sql.DB, query string, id int64) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err = rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		values[name] = value
	}
	return values, rows.Err()
}

func (db *DB) SetBrandSetting(ctx context.Context, brandID int64, name, value string) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO brand_settings(brand_id,name,value) VALUES(?,?,?) ON CONFLICT(brand_id,name) DO UPDATE SET value=excluded.value", brandID, name, value)
		return err
	})
}

func (db *DB) SetListSetting(ctx context.Context, listID int64, name, value string) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO list_settings(list_id,name,value) VALUES(?,?,?) ON CONFLICT(list_id,name) DO UPDATE SET value=excluded.value", listID, name, value)
		return err
	})
}
