ALTER TABLE user_brands ADD COLUMN daily_send_limit INTEGER CHECK (daily_send_limit IS NULL OR daily_send_limit >= 0);
ALTER TABLE campaigns ADD COLUMN created_by INTEGER REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX campaigns_created_by ON campaigns(created_by);
CREATE TABLE user_brand_daily (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
 day INTEGER NOT NULL,
 reserved INTEGER NOT NULL CHECK(reserved >= 0),
 PRIMARY KEY(user_id,brand_id,day)
) STRICT, WITHOUT ROWID;
CREATE INDEX user_brand_daily_brand ON user_brand_daily(brand_id);
CREATE TABLE campaign_user_reservations (
 campaign_id INTEGER PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
 day INTEGER NOT NULL,
 recipients INTEGER NOT NULL CHECK(recipients >= 0)
) STRICT;
CREATE INDEX campaign_user_reservations_user ON campaign_user_reservations(user_id);
CREATE INDEX campaign_user_reservations_brand ON campaign_user_reservations(brand_id);
CREATE TABLE totp_pending (
 token_hash BLOB PRIMARY KEY REFERENCES sessions(token_hash) ON DELETE CASCADE,
 secret TEXT NOT NULL,
 expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
