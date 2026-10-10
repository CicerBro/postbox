CREATE TABLE brands (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  from_name TEXT NOT NULL,
  from_email TEXT NOT NULL,
  reply_to TEXT,
  configuration_set TEXT,
  tracking_domain TEXT UNIQUE,
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE custom_fields (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  label TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('text','number','date','boolean','select')),
  is_filterable INTEGER NOT NULL DEFAULT 0,
  UNIQUE (brand_id, key)
) STRICT;

CREATE TABLE subscribers (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  email TEXT NOT NULL,
  domain TEXT GENERATED ALWAYS AS (substr(email, instr(email, '@') + 1)) VIRTUAL,
  name TEXT NOT NULL DEFAULT '',
  fields TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(fields)),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','bounced','complained','blocklisted')),
  soft_bounce_count INTEGER NOT NULL DEFAULT 0,
  gdpr_consent_at INTEGER,
  gdpr_consent_ip TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX subscribers_brand_email ON subscribers (brand_id, email);
CREATE INDEX subscribers_brand_domain ON subscribers (brand_id, domain);
CREATE INDEX subscribers_brand_created ON subscribers (brand_id, created_at);

CREATE TABLE lists (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  uuid TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  opt_in TEXT NOT NULL CHECK (opt_in IN ('single','double')),
  created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX lists_brand ON lists (brand_id);

CREATE TABLE list_subscribers (
  list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  status TEXT NOT NULL CHECK (status IN ('unconfirmed','subscribed','unsubscribed')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (list_id, subscriber_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX list_subscribers_subscriber ON list_subscribers (subscriber_id);
CREATE INDEX list_subscribers_active ON list_subscribers (list_id, subscriber_id) WHERE status = 'subscribed';
CREATE INDEX list_subscribers_unconfirmed ON list_subscribers (list_id, created_at) WHERE status = 'unconfirmed';

CREATE TABLE segments (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  rules TEXT NOT NULL CHECK (json_valid(rules)),
  created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX segments_brand ON segments (brand_id);

CREATE TABLE suppressions (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER REFERENCES brands(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('email','domain')),
  value TEXT NOT NULL,
  reason TEXT NOT NULL CHECK (reason IN ('hard_bounce','soft_bounce_limit','complaint','manual','ses_sync')),
  created_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX suppressions_kind_value_scope ON suppressions (kind, value, ifnull(brand_id, 0));
CREATE INDEX suppressions_brand ON suppressions (brand_id);

CREATE TABLE templates (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  html TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX templates_brand ON templates (brand_id);

CREATE TABLE campaigns (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  uuid TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK (status IN ('draft','scheduled','preparing','sending','paused','sent','cancelled')),
  subject TEXT NOT NULL,
  html TEXT NOT NULL,
  text TEXT,
  inline_css INTEGER NOT NULL DEFAULT 1,
  scheduled_at INTEGER,
  started_at INTEGER,
  finished_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX campaigns_brand_created ON campaigns (brand_id, created_at DESC);
CREATE INDEX campaigns_due ON campaigns (scheduled_at) WHERE status = 'scheduled';
CREATE INDEX campaigns_active ON campaigns (status) WHERE status IN ('preparing','sending','paused');

CREATE TABLE campaign_targets (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  target_type TEXT NOT NULL CHECK (target_type IN ('list','segment')),
  target_id INTEGER NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('include','exclude')),
  PRIMARY KEY (campaign_id, target_type, target_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE campaign_recipients (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','claimed','sent','failed','unknown','skipped')),
  attempts INTEGER NOT NULL DEFAULT 0,
  message_id TEXT,
  error TEXT,
  sent_at INTEGER,
  delivered_at INTEGER,
  first_opened_at INTEGER,
  first_human_opened_at INTEGER,
  first_clicked_at INTEGER,
  bounced_at INTEGER,
  complained_at INTEGER,
  unsubscribed_at INTEGER,
  PRIMARY KEY (campaign_id, subscriber_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX campaign_recipients_pending ON campaign_recipients (campaign_id, subscriber_id) WHERE status = 'pending';
CREATE INDEX campaign_recipients_claimed ON campaign_recipients (campaign_id) WHERE status = 'claimed';
CREATE UNIQUE INDEX campaign_recipients_message ON campaign_recipients (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX campaign_recipients_subscriber ON campaign_recipients (subscriber_id, campaign_id);

CREATE TABLE campaign_stats (
  campaign_id INTEGER PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE,
  recipients INTEGER NOT NULL DEFAULT 0,
  sent INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  delivered INTEGER NOT NULL DEFAULT 0,
  opens_total INTEGER NOT NULL DEFAULT 0,
  opens_unique INTEGER NOT NULL DEFAULT 0,
  opens_human_unique INTEGER NOT NULL DEFAULT 0,
  clicks_total INTEGER NOT NULL DEFAULT 0,
  clicks_unique INTEGER NOT NULL DEFAULT 0,
  unsubscribes INTEGER NOT NULL DEFAULT 0,
  bounces_hard INTEGER NOT NULL DEFAULT 0,
  bounces_soft INTEGER NOT NULL DEFAULT 0,
  complaints INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE links (
  id INTEGER PRIMARY KEY,
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  UNIQUE (campaign_id, url)
) STRICT;

CREATE TABLE opens (
  id INTEGER PRIMARY KEY,
  campaign_id INTEGER NOT NULL,
  subscriber_id INTEGER NOT NULL,
  at INTEGER NOT NULL,
  country TEXT,
  is_machine INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX opens_campaign_at ON opens (campaign_id, at);
CREATE INDEX opens_subscriber_at ON opens (subscriber_id, at);

CREATE TABLE clicks (
  id INTEGER PRIMARY KEY,
  campaign_id INTEGER NOT NULL,
  link_id INTEGER NOT NULL,
  subscriber_id INTEGER NOT NULL,
  at INTEGER NOT NULL,
  country TEXT,
  is_machine INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX clicks_campaign_link ON clicks (campaign_id, link_id);
CREATE INDEX clicks_campaign_at ON clicks (campaign_id, at);
CREATE INDEX clicks_subscriber_at ON clicks (subscriber_id, at);

CREATE TABLE ses_events (
  id INTEGER PRIMARY KEY,
  sns_message_id TEXT NOT NULL UNIQUE,
  ses_message_id TEXT NOT NULL,
  type TEXT NOT NULL,
  subtype TEXT,
  recipient TEXT NOT NULL,
  at INTEGER NOT NULL,
  payload TEXT NOT NULL
) STRICT;
CREATE INDEX ses_events_ses_message ON ses_events (ses_message_id);
CREATE INDEX ses_events_at ON ses_events (at);

CREATE TABLE list_daily_stats (
  list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  day INTEGER NOT NULL,
  active INTEGER NOT NULL,
  subscribed INTEGER NOT NULL DEFAULT 0,
  unsubscribed INTEGER NOT NULL DEFAULT 0,
  bounced INTEGER NOT NULL DEFAULT 0,
  complained INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (list_id, day)
) STRICT, WITHOUT ROWID;

CREATE TABLE send_hourly (
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  sent INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (brand_id, hour)
) STRICT, WITHOUT ROWID;

CREATE TABLE jobs (
  id INTEGER PRIMARY KEY,
  queue TEXT NOT NULL,
  payload TEXT NOT NULL CHECK (json_valid(payload)),
  status TEXT NOT NULL CHECK (status IN ('pending','running','done','failed')),
  run_at INTEGER NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  locked_until INTEGER,
  last_error TEXT,
  created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX jobs_due ON jobs (queue, run_at) WHERE status = 'pending';
CREATE INDEX jobs_stale ON jobs (locked_until) WHERE status = 'running';

CREATE TABLE webhooks (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  events TEXT NOT NULL CHECK (json_valid(events)),
  secret TEXT NOT NULL,
  is_active INTEGER NOT NULL DEFAULT 1
) STRICT;
CREATE INDEX webhooks_brand ON webhooks (brand_id);

CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  totp_secret TEXT,
  role TEXT NOT NULL CHECK (role IN ('admin','brand_user')),
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE user_brands (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  permissions TEXT NOT NULL CHECK (json_valid(permissions)),
  PRIMARY KEY (user_id, brand_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX user_brands_brand ON user_brands (brand_id);

CREATE TABLE sessions (
  token_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE api_keys (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL UNIQUE,
  secret_hash BLOB NOT NULL,
  scopes TEXT NOT NULL CHECK (json_valid(scopes)),
  last_used_at INTEGER,
  revoked_at INTEGER,
  created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX api_keys_brand ON api_keys (brand_id);
