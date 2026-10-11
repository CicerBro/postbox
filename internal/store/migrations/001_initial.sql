-- Fresh-install schema. Future changes belong in new numbered migrations.

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
  updated_at INTEGER NOT NULL,
  marketing_unsubscribed_at INTEGER
) STRICT;
CREATE UNIQUE INDEX subscribers_brand_email ON subscribers (brand_id, email);
CREATE INDEX subscribers_brand_domain ON subscribers (brand_id, domain);
CREATE INDEX subscribers_brand_created ON subscribers (brand_id, created_at);
CREATE INDEX subscribers_brand_id ON subscribers(brand_id,id);
CREATE INDEX subscribers_email ON subscribers(email);
CREATE INDEX subscribers_brand_status_id ON subscribers(brand_id,status,id);
CREATE INDEX subscribers_brand_name_id ON subscribers(brand_id,name,id);
CREATE INDEX subscribers_brand_consent_id ON subscribers(brand_id,coalesce(gdpr_consent_at,0),id);

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
  confirmation_started_at INTEGER,
  PRIMARY KEY (list_id, subscriber_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX list_subscribers_subscriber ON list_subscribers (subscriber_id);
CREATE INDEX list_subscribers_active ON list_subscribers (list_id, subscriber_id) WHERE status = 'subscribed';
CREATE INDEX list_subscribers_unconfirmed ON list_subscribers (list_id, created_at) WHERE status = 'unconfirmed';
CREATE INDEX list_subscribers_unconfirmed_started ON list_subscribers(COALESCE(confirmation_started_at,updated_at)) WHERE status='unconfirmed';

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
  updated_at INTEGER NOT NULL,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL
) STRICT;
CREATE INDEX campaigns_brand_created ON campaigns (brand_id, created_at DESC);
CREATE INDEX campaigns_due ON campaigns (scheduled_at) WHERE status = 'scheduled';
CREATE INDEX campaigns_active ON campaigns (status) WHERE status IN ('preparing','sending','paused');
CREATE INDEX campaigns_brand_id ON campaigns(brand_id,id);
CREATE INDEX campaigns_created_by ON campaigns(created_by);

CREATE TABLE campaign_targets (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  target_type TEXT NOT NULL CHECK (target_type IN ('list','segment')),
  target_id INTEGER NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('include','exclude')),
  PRIMARY KEY (campaign_id, target_type, target_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX campaign_targets_target ON campaign_targets(target_type,target_id,campaign_id);

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
  first_human_clicked_at INTEGER,
  snapshot_email TEXT,
  snapshot_name TEXT,
  snapshot_fields TEXT CHECK(snapshot_fields IS NULL OR json_valid(snapshot_fields)),
  PRIMARY KEY (campaign_id, subscriber_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX campaign_recipients_pending ON campaign_recipients (campaign_id, subscriber_id) WHERE status = 'pending';
CREATE INDEX campaign_recipients_claimed ON campaign_recipients (campaign_id) WHERE status = 'claimed';
CREATE UNIQUE INDEX campaign_recipients_message ON campaign_recipients (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX campaign_recipients_subscriber ON campaign_recipients (subscriber_id, campaign_id);
CREATE INDEX campaign_recipients_status ON campaign_recipients(campaign_id,status,subscriber_id);
CREATE INDEX campaign_recipients_recent_opened ON campaign_recipients(campaign_id,first_opened_at DESC,subscriber_id DESC) WHERE first_opened_at IS NOT NULL;
CREATE INDEX campaign_recipients_recent_unsubscribed ON campaign_recipients(campaign_id,unsubscribed_at DESC,subscriber_id DESC) WHERE unsubscribed_at IS NOT NULL;
CREATE INDEX campaign_recipients_recent_bounced ON campaign_recipients(campaign_id,bounced_at DESC,subscriber_id DESC) WHERE bounced_at IS NOT NULL;
CREATE INDEX campaign_recipients_recent_complained ON campaign_recipients(campaign_id,complained_at DESC,subscriber_id DESC) WHERE complained_at IS NOT NULL;
CREATE INDEX campaign_recipients_sent_at ON campaign_recipients(campaign_id,sent_at,subscriber_id) WHERE sent_at IS NOT NULL;
CREATE INDEX campaign_recipients_snapshot_email ON campaign_recipients(campaign_id,snapshot_email);

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
  complaints INTEGER NOT NULL DEFAULT 0,
  skipped INTEGER NOT NULL DEFAULT 0,
  unknown INTEGER NOT NULL DEFAULT 0,
  opens_human_total INTEGER NOT NULL DEFAULT 0,
  clicks_human_total INTEGER NOT NULL DEFAULT 0,
  clicks_human_unique INTEGER NOT NULL DEFAULT 0
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
CREATE INDEX opens_retention ON opens(at,id);

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
CREATE INDEX clicks_retention ON clicks(at,id);
CREATE INDEX clicks_link ON clicks(link_id,id);

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
CREATE INDEX jobs_failed ON jobs(id) WHERE status='failed';
CREATE INDEX jobs_active_queue ON jobs(queue,run_at) WHERE status IN ('pending','running');

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
  daily_send_limit INTEGER CHECK (daily_send_limit IS NULL OR daily_send_limit >= 0),
  PRIMARY KEY (user_id, brand_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX user_brands_brand ON user_brands (brand_id);

CREATE TABLE sessions (
  token_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL,
  csrf_hash BLOB NOT NULL DEFAULT X''
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

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT, WITHOUT ROWID;
INSERT INTO settings(key,value) VALUES ('sending_enabled','true'), ('dispatch_ledger_bootstrap','0:0');

CREATE TABLE campaign_preparations (
  campaign_id INTEGER PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE,
  subject TEXT NOT NULL,
  html TEXT NOT NULL,
  text TEXT NOT NULL,
  from_name TEXT NOT NULL,
  from_email TEXT NOT NULL,
  reply_to TEXT NOT NULL DEFAULT '',
  configuration_set TEXT NOT NULL DEFAULT '',
  tracking_url TEXT NOT NULL,
  fields TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(fields)),
  prepared_at INTEGER NOT NULL,
  recipients_snapshotted INTEGER NOT NULL DEFAULT 0 CHECK(recipients_snapshotted IN (0,1))
) STRICT;

CREATE TABLE send_attempts (
  id INTEGER PRIMARY KEY,
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  attempt INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  outcome TEXT NOT NULL DEFAULT 'started' CHECK(outcome IN ('started','sent','failed','retry','unknown')),
  error TEXT,
  message_id TEXT,
  dispatch_generation INTEGER NOT NULL DEFAULT 0,
  UNIQUE(campaign_id,subscriber_id,attempt)
) STRICT;
CREATE INDEX send_attempts_subscriber ON send_attempts(subscriber_id,campaign_id);
CREATE INDEX send_attempts_unfinished ON send_attempts(campaign_id,id) WHERE finished_at IS NULL;

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY,
  user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  brand_id INTEGER REFERENCES brands(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id INTEGER,
  detail TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(detail)),
  at INTEGER NOT NULL
) STRICT;
CREATE INDEX audit_log_user ON audit_log(user_id,id);
CREATE INDEX audit_log_brand ON audit_log(brand_id,id);

CREATE TABLE link_stats (
  link_id INTEGER PRIMARY KEY REFERENCES links(id) ON DELETE CASCADE,
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  clicks_total INTEGER NOT NULL DEFAULT 0,
  clicks_unique INTEGER NOT NULL DEFAULT 0,
  clicks_human_total INTEGER NOT NULL DEFAULT 0,
  clicks_human_unique INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX link_stats_campaign ON link_stats(campaign_id,link_id);

CREATE TABLE campaign_countries (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  country TEXT NOT NULL,
  opens INTEGER NOT NULL DEFAULT 0,
  clicks INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(campaign_id,country)
) STRICT, WITHOUT ROWID;

CREATE TABLE engagement_hourly (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  opens INTEGER NOT NULL DEFAULT 0,
  clicks INTEGER NOT NULL DEFAULT 0,
  deliveries INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(campaign_id,hour)
) STRICT, WITHOUT ROWID;

CREATE TABLE recipient_link_stats (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  link_id INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
  first_clicked_at INTEGER NOT NULL,
  first_human_clicked_at INTEGER,
  PRIMARY KEY(campaign_id,subscriber_id,link_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX recipient_link_stats_subscriber ON recipient_link_stats(subscriber_id,campaign_id);
CREATE INDEX recipient_link_stats_link ON recipient_link_stats(link_id,subscriber_id);

CREATE TABLE subscription_confirmations (
  token_hash BLOB PRIMARY KEY,
  list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL,
  resubscribe_brand INTEGER NOT NULL DEFAULT 0
) STRICT, WITHOUT ROWID;
CREATE INDEX subscription_confirmations_list ON subscription_confirmations(list_id);
CREATE INDEX subscription_confirmations_subscriber ON subscription_confirmations(subscriber_id);
CREATE INDEX subscription_confirmations_expires ON subscription_confirmations(expires_at);

CREATE TABLE subscriber_activity (
  id INTEGER PRIMARY KEY,
  brand_id INTEGER NOT NULL,
  subscriber_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  at INTEGER NOT NULL,
  detail TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(detail))
) STRICT;
CREATE INDEX subscriber_activity_subscriber ON subscriber_activity(subscriber_id,id);
CREATE INDEX subscriber_activity_brand ON subscriber_activity(brand_id,id);

CREATE TABLE list_counters (
  list_id INTEGER PRIMARY KEY REFERENCES lists(id) ON DELETE CASCADE,
  active INTEGER NOT NULL DEFAULT 0,
  subscribed INTEGER NOT NULL DEFAULT 0,
  unsubscribed INTEGER NOT NULL DEFAULT 0,
  bounced INTEGER NOT NULL DEFAULT 0,
  complained INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE migration_identities (
  source TEXT NOT NULL,
  entity TEXT NOT NULL,
  source_id TEXT NOT NULL,
  target_id INTEGER NOT NULL,
  PRIMARY KEY(source,entity,source_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX migration_identities_target ON migration_identities(entity,target_id);

CREATE TABLE migration_records (
  source TEXT NOT NULL,
  entity TEXT NOT NULL,
  source_id TEXT NOT NULL,
  payload TEXT NOT NULL CHECK(json_valid(payload)),
  PRIMARY KEY(source,entity,source_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE list_counter_snapshots (
  list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  day INTEGER NOT NULL,
  subscribed INTEGER NOT NULL DEFAULT 0,
  unsubscribed INTEGER NOT NULL DEFAULT 0,
  bounced INTEGER NOT NULL DEFAULT 0,
  complained INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(list_id,day)
) STRICT, WITHOUT ROWID;

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

-- These private fingerprints deliberately have no subscriber foreign key.

CREATE TABLE campaign_dispatch_ledger (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  email_hash BLOB NOT NULL CHECK(length(email_hash)=32),
  outcome TEXT NOT NULL CHECK(outcome IN ('started','sent','unknown','rejected')),
  generation INTEGER NOT NULL CHECK(generation>0),
  owner_subscriber_id INTEGER NOT NULL,
  owner_attempt INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(campaign_id,email_hash)
) STRICT, WITHOUT ROWID;
CREATE INDEX campaign_dispatch_owner ON campaign_dispatch_ledger(campaign_id,owner_subscriber_id,owner_attempt);

CREATE TABLE passkey_users (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  handle BLOB NOT NULL UNIQUE CHECK(length(handle)=32)
) STRICT;

CREATE TABLE passkeys (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  rp_id TEXT NOT NULL,
  credential_id BLOB NOT NULL,
  credential TEXT NOT NULL CHECK(json_valid(credential)),
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  UNIQUE(rp_id,credential_id)
) STRICT;
CREATE INDEX passkeys_user ON passkeys(user_id,rp_id);

CREATE TABLE passkey_ceremonies (
  binding_hash BLOB PRIMARY KEY,
  kind TEXT NOT NULL CHECK(kind IN ('register','login')),
  user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
  session_hash BLOB,
  auth_hash BLOB,
  name TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL CHECK(json_valid(state)),
  expires_at INTEGER NOT NULL
) STRICT;
CREATE INDEX passkey_ceremonies_expiry ON passkey_ceremonies(expires_at);
CREATE INDEX passkey_ceremonies_user ON passkey_ceremonies(user_id);

CREATE TABLE campaign_lifecycle_events (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  event TEXT NOT NULL CHECK (event IN ('campaign_started','campaign_finished')),
  emitted_at INTEGER NOT NULL,
  PRIMARY KEY (campaign_id,event)
) STRICT, WITHOUT ROWID;

CREATE TABLE "brand_aws_settings" (
  brand_id INTEGER PRIMARY KEY REFERENCES brands(id) ON DELETE CASCADE,
  mode TEXT NOT NULL CHECK(mode='static'),
  region TEXT NOT NULL,
  credentials BLOB NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL,
  sns_topic_arn TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE UNIQUE INDEX brand_aws_topic ON brand_aws_settings(sns_topic_arn) WHERE sns_topic_arn!='';

CREATE TABLE recurring_jobs (
  queue TEXT PRIMARY KEY,
  interval_ms INTEGER NOT NULL CHECK(interval_ms > 0),
  next_run_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
