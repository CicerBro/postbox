CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT, WITHOUT ROWID;
INSERT INTO settings(key,value) VALUES ('sending_enabled','true');

CREATE TABLE campaign_preparations (
 campaign_id INTEGER PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE,
 subject TEXT NOT NULL, html TEXT NOT NULL, text TEXT NOT NULL,
 from_name TEXT NOT NULL, from_email TEXT NOT NULL, reply_to TEXT NOT NULL DEFAULT '',
 configuration_set TEXT NOT NULL DEFAULT '', tracking_url TEXT NOT NULL,
 fields TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(fields)), prepared_at INTEGER NOT NULL
) STRICT;

CREATE TABLE send_attempts (
 id INTEGER PRIMARY KEY,
 campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
 subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
 attempt INTEGER NOT NULL,
 started_at INTEGER NOT NULL, finished_at INTEGER,
 outcome TEXT NOT NULL DEFAULT 'started' CHECK(outcome IN ('started','sent','failed','retry','unknown')),
 error TEXT, message_id TEXT,
 UNIQUE(campaign_id,subscriber_id,attempt)
) STRICT;
CREATE INDEX send_attempts_subscriber ON send_attempts(subscriber_id,campaign_id);
CREATE INDEX send_attempts_unfinished ON send_attempts(campaign_id,id) WHERE finished_at IS NULL;

CREATE TABLE audit_log (
 id INTEGER PRIMARY KEY,
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 brand_id INTEGER REFERENCES brands(id) ON DELETE SET NULL,
 action TEXT NOT NULL, entity_type TEXT NOT NULL, entity_id INTEGER,
 detail TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(detail)), at INTEGER NOT NULL
) STRICT;
CREATE INDEX audit_log_user ON audit_log(user_id,id);
CREATE INDEX audit_log_brand ON audit_log(brand_id,id);

CREATE INDEX subscribers_brand_id ON subscribers(brand_id,id);
CREATE INDEX subscribers_email ON subscribers(email);
CREATE INDEX campaigns_brand_id ON campaigns(brand_id,id);
CREATE INDEX campaign_recipients_status ON campaign_recipients(campaign_id,status,subscriber_id);
CREATE INDEX campaign_targets_target ON campaign_targets(target_type,target_id,campaign_id);
CREATE INDEX opens_retention ON opens(at,id);
CREATE INDEX clicks_retention ON clicks(at,id);
CREATE INDEX clicks_link ON clicks(link_id,id);

ALTER TABLE campaign_stats ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;
ALTER TABLE campaign_stats ADD COLUMN unknown INTEGER NOT NULL DEFAULT 0;
ALTER TABLE campaign_stats ADD COLUMN opens_human_total INTEGER NOT NULL DEFAULT 0;
ALTER TABLE campaign_stats ADD COLUMN clicks_human_total INTEGER NOT NULL DEFAULT 0;
ALTER TABLE campaign_stats ADD COLUMN clicks_human_unique INTEGER NOT NULL DEFAULT 0;
ALTER TABLE campaign_recipients ADD COLUMN first_human_clicked_at INTEGER;
ALTER TABLE campaign_recipients ADD COLUMN snapshot_email TEXT;
ALTER TABLE campaign_recipients ADD COLUMN snapshot_name TEXT;
ALTER TABLE campaign_recipients ADD COLUMN snapshot_fields TEXT CHECK(snapshot_fields IS NULL OR json_valid(snapshot_fields));

CREATE TABLE link_stats (
 link_id INTEGER PRIMARY KEY REFERENCES links(id) ON DELETE CASCADE,
 campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
 clicks_total INTEGER NOT NULL DEFAULT 0, clicks_unique INTEGER NOT NULL DEFAULT 0,
 clicks_human_total INTEGER NOT NULL DEFAULT 0, clicks_human_unique INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX link_stats_campaign ON link_stats(campaign_id,link_id);
CREATE TABLE campaign_countries (
 campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
 country TEXT NOT NULL, opens INTEGER NOT NULL DEFAULT 0, clicks INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(campaign_id,country)
) STRICT, WITHOUT ROWID;
CREATE TABLE engagement_hourly (
 campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
 hour INTEGER NOT NULL, opens INTEGER NOT NULL DEFAULT 0, clicks INTEGER NOT NULL DEFAULT 0,
 deliveries INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(campaign_id,hour)
) STRICT, WITHOUT ROWID;
CREATE TABLE recipient_link_stats (
 campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
 subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
 link_id INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
 first_clicked_at INTEGER NOT NULL, first_human_clicked_at INTEGER,
 PRIMARY KEY(campaign_id,subscriber_id,link_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX recipient_link_stats_subscriber ON recipient_link_stats(subscriber_id,campaign_id);
CREATE INDEX recipient_link_stats_link ON recipient_link_stats(link_id,subscriber_id);

CREATE TABLE subscription_confirmations (
 token_hash BLOB PRIMARY KEY,
 list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
 subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
 expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX subscription_confirmations_list ON subscription_confirmations(list_id);
CREATE INDEX subscription_confirmations_subscriber ON subscription_confirmations(subscriber_id);
CREATE INDEX subscription_confirmations_expires ON subscription_confirmations(expires_at);
CREATE TABLE subscriber_activity (
 id INTEGER PRIMARY KEY, brand_id INTEGER NOT NULL, subscriber_id INTEGER NOT NULL,
 kind TEXT NOT NULL, at INTEGER NOT NULL, detail TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(detail))
) STRICT;
CREATE INDEX subscriber_activity_subscriber ON subscriber_activity(subscriber_id,id);
CREATE INDEX subscriber_activity_brand ON subscriber_activity(brand_id,id);
CREATE TABLE list_counters (
 list_id INTEGER PRIMARY KEY REFERENCES lists(id) ON DELETE CASCADE,
 active INTEGER NOT NULL DEFAULT 0, subscribed INTEGER NOT NULL DEFAULT 0,
 unsubscribed INTEGER NOT NULL DEFAULT 0, bounced INTEGER NOT NULL DEFAULT 0,
 complained INTEGER NOT NULL DEFAULT 0
) STRICT;
ALTER TABLE sessions ADD COLUMN csrf_hash BLOB NOT NULL DEFAULT X'';
CREATE TABLE migration_identities (
 source TEXT NOT NULL, entity TEXT NOT NULL, source_id TEXT NOT NULL, target_id INTEGER NOT NULL,
 PRIMARY KEY(source,entity,source_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX migration_identities_target ON migration_identities(entity,target_id);
CREATE TABLE migration_records (
 source TEXT NOT NULL, entity TEXT NOT NULL, source_id TEXT NOT NULL,
 payload TEXT NOT NULL CHECK(json_valid(payload)),
 PRIMARY KEY(source,entity,source_id)
) STRICT, WITHOUT ROWID;
