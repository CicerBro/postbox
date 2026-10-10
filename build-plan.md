# Build "Postbox": a fast, self-hosted newsletter platform (Sendy/Listmonk replacement)

## Context
I currently run Sendy (PHP/MySQL) and want to replace it with a custom app written in Go. It must be noticeably faster than Sendy, easy to run, and focused on one job: sending newsletters through Amazon SES and handling everything that follows (bounces, complaints, unsubscribes, reporting).

- About 350,000 subscribers, about 4 campaigns per year (send-day reliability matters more than daily throughput).
- My SES max send rate is currently 175/sec. Never hardcode it: read it from the SES v2 `GetAccount` API (`SendQuota.MaxSendRate`, `Max24HourSend`, `SentLast24Hours`).
- At 175/sec, 350k recipients should take about 33 minutes. The sender must hold the account rate steadily for the whole run and scale automatically if AWS raises my quota.
- My newsletter templates currently live in Sendy as full HTML/CSS documents. They will live in this app; SES stored templates are never used.

## Hard constraints
- **Single Go binary, no external services at runtime.** Embed the frontend assets (`go:embed`) and migrations. No Redis, no Postgres, no external broker. Asynq is not allowed: the library compiles in, but it needs a Redis server. River is not allowed: it needs Postgres.
- **SQLite in WAL mode** is the only datastore and also the durable queue. Use a CGO-free driver (e.g. `modernc.org/sqlite`) so cross-compiling stays trivial. See the "Database" section.
- All sending happens inside this process with the **SES v2 `SendEmail` API using `Content.Raw`**: the app builds the complete email (MIME) in Go. Do not use SMTP, `SendBulkEmail`, or SES templates.
- Frontend tooling uses **Bun** for everything (install, build, scripts). No npm/yarn/pnpm.
- No AI features. No autoresponders or drip campaigns.

## Sending engine (the most important part)

### Rendering: once per campaign, cheap per recipient
- When a campaign starts, prepare it exactly once:
  1. Resolve the template and merge tags.
  2. Optionally inline the CSS (toggle per campaign).
  3. Rewrite links for click tracking and give each link an ID.
  4. Generate the plain-text part from the HTML.
  5. Compile the subject, HTML and text into a list of fixed byte chunks with placeholder slots between them (name, email, custom fields, tracking token, unsubscribe URL, web version URL). Pre-resolve fallbacks so they cost nothing at send time.
- Per recipient: write the fixed chunks plus that recipient's values (HTML-escaped in the HTML part) into a pooled buffer, then build the MIME message.
- The same renderer produces sent emails, the web version, previews and test sends, so the web version is always byte-identical to what was sent.
- Add benchmarks: rendering plus building the MIME message must take a tiny fraction of the per-message time budget (target well under 1ms for a 100KB email) and allocate little. CPU must never be the bottleneck.

### The MIME message
- `multipart/alternative` with `text/plain` and `text/html`, both UTF-8 and quoted-printable. No line over 998 characters.
- Non-ASCII subject and display names are encoded per RFC 2047. Recipient domains are punycoded; reject addresses whose local part isn't ASCII (SES doesn't support SMTPUTF8).
- Headers: `From`, `To`, `Reply-To`, `Subject`, `Date`, `MIME-Version`, plus per recipient:
  - `List-Unsubscribe: <https://{tracking-domain}/u/{token}>` (optionally also a `mailto:`)
  - `List-Unsubscribe-Post: List-Unsubscribe=One-Click` (RFC 8058)
- This must satisfy Gmail's and Yahoo's bulk-sender requirements. The one-click POST endpoint must unsubscribe immediately: no login, no cookies, no confirmation page.
- DKIM signing is done by SES (Easy DKIM); don't sign in the app.
- Call `SendEmail` with `FromEmailAddress`, an explicit `Destination.ToAddresses`, `Content.Raw.Data`, `ConfigurationSetName`, and `EmailTags` (brand ID, campaign ID) so SES events can be matched back to the database. Store the returned `MessageId` per recipient.
- Put sending behind a `Sender` interface, so a bulk or SMTP sender could be added later without touching the rest.

### Rate control and concurrency
- A token bucket counted per message (`golang.org/x/time/rate`). The fill rate is `MaxSendRate`; the burst is capped at `MaxSendRate`, so never more than one second of burst.
- Re-read `GetAccount` at the start of every campaign and every few minutes during it, and adjust the bucket live.
- Before starting, check `Max24HourSend - SentLast24Hours` against the campaign size. If the campaign won't fit, warn me and let me schedule it or send it in parts.
- A worker pool of concurrent `SendEmail` calls, sized dynamically to about rate × p99 latency (with a cap), sharing one keep-alive HTTP client.
  - Set `MaxIdleConnsPerHost` to at least the maximum number of workers; Go's and the SDK's defaults are too low and would cause connection churn.
  - Limit the SDK's built-in retries and turn off its client-side retry rate limiter, so the app's own limiter and backoff stay in control.
- On `TooManyRequestsException` or throttling errors: back off with jitter, temporarily lower the effective rate (additive increase, multiplicative decrease), then recover toward the account rate.
- Error classes:
  - `MessageRejected` or an invalid address → mark that recipient failed and continue.
  - `AccountSuspendedException`, `SendingPausedException`, `MailFromDomainNotVerifiedException` → pause the campaign immediately and alert me.
  - Network errors, 5xx and timeouts → retry with backoff. A timeout may already have been accepted by SES, so record the attempt.
- Before sending, show the estimated upload volume (message size × recipients) and the expected duration.

### Pipeline
All stages are goroutines connected by bounded channels, so memory stays flat:
1. **Prepare**: snapshot the recipients into `campaign_recipients` with a single `INSERT ... SELECT`. Apply list/segment include and exclude rules, deduplicate across lists, and remove inactive subscribers and email/domain suppressions. The total recipient count is then known before sending starts.
2. **Claimer**: take the next ~200 `pending` rows by keyset (`subscriber_id > last ORDER BY subscriber_id LIMIT n`, using the partial index), mark them `claimed` and commit, *then* hand them on. This is about one second of work in flight.
3. **Render**: fill in each recipient and build the MIME message.
4. **Limiter**: take one token per message.
5. **Workers**: perform `SendEmail`.
6. **Results writer**: one goroutine that applies `sent`/`failed` + `MessageId` + `sent_at` in batched transactions (every ~500 rows or ~250ms) using prepared statements, and updates the campaign counters in the same transaction.

### Correctness
- Recipient state: `pending → claimed → sent | failed`, plus `skipped` and `unknown`. Pause, resume and cancel must work at any time. After a crash or restart, resume where the send left off.
- On startup, rows still `claimed` have an unknown outcome. Make the policy configurable. The default marks them `unknown` and does **not** resend (a missed email is better than a duplicate); the report lists them with a "resend these" action.
- If someone unsubscribes, bounces or complains while a campaign is sending, their `pending` rows in active campaigns become `skipped`.
- Graceful shutdown (SIGTERM): stop claiming, let in-flight requests finish, flush the results writer, then exit.
- A global "Sending on/off" kill switch in the UI pauses everything immediately.

## Database (SQLite)

### Connections and settings
- One dedicated **writer** connection (`SetMaxOpenConns(1)`) that all writes go through, and a separate pool of **reader** connections. Hot write paths batch work in transactions.
- Set on every connection:
  - `PRAGMA journal_mode = WAL` (persists in the file)
  - `PRAGMA synchronous = NORMAL`
  - `PRAGMA foreign_keys = ON`
  - `PRAGMA busy_timeout = 5000`
  - `PRAGMA temp_store = MEMORY`
  - `PRAGMA cache_size = -65536` (64MB)
  - `PRAGMA mmap_size = 268435456`
- Write transactions start with `BEGIN IMMEDIATE`. Run `ANALYZE` after large imports, and `PRAGMA optimize` periodically and on shutdown.
- Use `STRICT` tables. Timestamps are `INTEGER` unix milliseconds. Hot tables use `INTEGER PRIMARY KEY`.
- Emails are normalized when written (trimmed, lowercased, domain punycoded), so lookups never wrap indexed columns in functions.

### Indexing rules
- Every foreign-key column has an index. SQLite doesn't create them automatically, and without them cascades and joins scan the whole table.
- Work queues (pending recipients, due campaigns, due jobs, webhook retries) use **partial indexes**, so they stay tiny no matter how much history piles up.
- No `OFFSET` anywhere; all list views and batch jobs use keyset pagination.
- Dashboards and reports read **pre-aggregated counter and rollup tables**, never `COUNT(*)` over event tables on page load.
- Custom fields live in a JSON column. When a field is marked "filterable", create an expression index on it, e.g. `(brand_id, fields ->> '$.country')`, and have segment SQL use exactly the same expression.
- Raw event tables (opens, clicks, SES events) have no FK cascades; deletes are done by background jobs in batches. A retention job purges raw events after a configurable period, keeping the aggregates and the `first_*` timestamps.
- **Add a test** that runs `EXPLAIN QUERY PLAN` on every hot query (claimer, results writer, recipient snapshot, MessageId lookup, subscriber lookup by email, list views, segment counts, report queries, due jobs) and fails if any of them does a full `SCAN` of a large table: subscribers, list_subscribers, campaign_recipients, opens, clicks, ses_events.

### Starting schema
Refine as needed, but keep the indexes or replace them with something demonstrably better.

```sql
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
```

### Background jobs
- The `campaign_recipients` table *is* the send queue; nothing else is needed for sending.
- Everything else (scheduled campaign start, CSV imports, outbound webhooks with retries, rollups, housekeeping, retention, SES suppression sync) runs on a small in-process job runner backed by the `jobs` table.
  - It claims jobs atomically with `UPDATE ... RETURNING`, holding a lease via `locked_until`.
  - Retries use exponential backoff.
  - Stale `running` jobs are recovered on startup.

### Writes from public endpoints
- Open, click and unsubscribe events are pushed onto an in-memory channel and written by a batching writer, so tracking endpoints never wait on SQLite.
- The batching writer also updates `campaign_stats`, `campaign_recipients.first_*_at`, `send_hourly` and the related counters in the same transaction.
- If the channel is full, drop raw open events (never clicks or unsubscribes) and count the drops in metrics.

## Bounces, complaints and suppressions
- Create or verify an SES configuration set that publishes Bounce, Complaint, Delivery, Reject and DeliveryDelay events to SNS.
- Primary ingestion is **SNS → SQS, long-polled by the binary**, so events are never lost while the app is down and no public webhook is needed. Also support a direct SNS HTTPS endpoint with SNS signature verification and automatic subscription confirmation.
- Deduplicate on the SNS message ID; match events to recipients via the SES `MessageId`, with email tags as a fallback.
- Permanent bounce → suppress globally. Transient bounce → increment the soft-bounce count, and suppress after a configurable threshold. Complaint → suppress globally and unsubscribe from all lists.
- Keep a global suppression list of emails *and* whole domains, imported from and optionally synced with the SES account-level suppression list (`ListSuppressedDestinations`).
- Everything updates campaign reports in real time.

## Merge tags
- Support Sendy-compatible tags so my existing templates work unchanged: `[Name,fallback=there]`, `[Email]`, `[webversion]`, `[unsubscribe]`, `[currentdaydate]` and similar, and any custom field as `[FieldName,fallback=...]`.
- Also support a cleaner `{{ name | "there" }}` syntax.
- Values are HTML-escaped in HTML context and left raw in the text part and headers (with header injection prevented).
- The editor validates tags and flags unknown fields before sending.

## Tracking, web version, unsubscribe
- Open pixel and click redirects use per-recipient tokens: a compact encoding of campaign and subscriber IDs plus a truncated HMAC, so tokens can't be enumerated or forged. Link IDs are decided once at render time; only the token varies per recipient.
- Click redirects must be very fast (in-memory link map per active campaign, loaded lazily for older campaigns) and log asynchronously.
- Flag likely machine opens and clicks (Apple Mail Privacy Protection, link scanners, opens or clicks within seconds of delivery) and show "human" numbers separately.
- Optional GeoIP for opens and clicks using a local MaxMind GeoLite2 file.
- Web version: `/w/{campaign}/{token}` renders the personalized email in the browser; `/w/{campaign}` renders a generic public archive version.
- Unsubscribe page: per-list or all-lists, a customizable goodbye page, an optional goodbye email, and a re-subscribe option.
- Custom domain per brand for tracking, web version and unsubscribe links.

## Features (Sendy parity, minus AI and autoresponders)
- **Brands**: multiple brands, each with its own from-name, from-email, reply-to, SES configuration set, tracking domain, lists, templates and reports. A brand switcher sits at the top of the UI. Brand users/clients get scoped logins, permissions and optional sending limits.
- **Lists and subscribers**: custom fields (text, number, date, boolean, select), single or double opt-in with a customizable confirmation email, thank-you and goodbye pages and emails.
  - CSV import with field mapping and a dry-run preview. Imports are streamed and written in large transactions with prepared statements; 350k rows must import in seconds.
  - Mass delete and unsubscribe by pasting emails or uploading a CSV. Export. Per-subscriber activity timeline.
- **Segments**: a rule builder on fields, list membership, engagement (opened or clicked campaign X, inactive N campaigns), signup date and GDPR consent. Rules compile to indexed SQL; show a live count preview.
- **Campaigns**:
  - Send to multiple lists and segments with include/exclude. Test sends to several addresses. Schedule with timezone. Duplicate.
  - States: draft → scheduled → preparing → sending → paused → sent/cancelled.
  - A pre-send checklist covering missing tags, broken links, size above Gmail's 102KB clipping limit, the daily quota, suppressed/skipped counts, estimated duration and upload volume.
- **Live reports**: sent, delivered, opens (unique/total/human), clicks per link, unsubscribes, bounces (hard/soft), complaints, countries, an activity-over-time chart, and failed/unknown recipients. Updates live while sending, via SSE. CSV export.
- **Templates**: a library of reusable HTML templates per brand.
- **Subscribe forms**: embeddable HTML snippet and hosted page, honeypot plus optional Cloudflare Turnstile, GDPR consent checkbox stored with timestamp and IP.
- **Housekeeping**: one click to remove unconfirmed double opt-ins older than N days, and to remove or segment subscribers inactive for N campaigns.
- **Rules and webhooks**: outbound webhooks (HMAC-signed, with retries via the job runner) for subscribe, unsubscribe, bounce, complaint, campaign started and campaign finished. Email notifications when a campaign starts or finishes.
- **Sendy migration**: an importer that reads a Sendy MySQL database or a SQL dump and imports brands, lists, custom fields, subscribers *with their status* (unsubscribed, bounced, complained, unconfirmed), templates and past campaigns. Never email someone Sendy had suppressed.

## REST API
- JSON over HTTP under `/api/v1`, API keys scoped per brand with read/write scopes (stored as prefix + hash), an OpenAPI 3 spec served by the app, rate-limited.
- Endpoints for lists (CRUD) and subscribers: create or upsert by email, update fields and lists, unsubscribe, delete (GDPR erase, including opens/clicks), get by email, search with keyset pagination. Also bulk upsert, campaign read and stats.
- Idempotent upserts (`INSERT ... ON CONFLICT DO UPDATE`), so my app can call them on every signup or profile edit.
- Optionally provide a Sendy-compatible shim (`/subscribe`, `/unsubscribe`, `/api/subscribers/subscription-status.php`, `/api/subscribers/active-subscriber-count.php`) so existing integrations keep working during migration.

## Frontend
- React SPA built with Bun, using **shadcn/ui** and **Tailwind CSS v4**, embedded in the binary. Use TanStack Query for data fetching and SSE for live campaign progress and reports.
- Look and feel, based on Mailcheap's overview page:
  - A clean monochrome light theme with lots of whitespace, plus dark mode.
  - Left sidebar: Overview, Campaigns, Contacts/Lists, Templates, Segments, Domains, API keys, Settings. A "Sending on/off" status pill at the bottom.
  - Brand switcher tabs at the top and a primary "+ New" dropdown.
  - Overview page: a KPI strip (Sent, Delivered %, Opened %, Clicked %, Bounced %, Complaints %, each with its change vs the previous period), a subscriber growth line chart comparing the last 30 days with the previous 30 days (from `list_daily_stats`), and an "emails sent per hour, today vs yesterday" chart (from `send_hourly`).
- **Email editor**:
  - Raw HTML is the source of truth: my templates are full HTML documents with `<style>` in the head, table layouts, inline styles and MSO conditional comments.
  - HTML mode: CodeMirror 6 with a live preview in a sandboxed iframe, a desktop/mobile toggle and a merge-tag picker.
  - WYSIWYG mode: evaluate GrapesJS (newsletter preset) vs TinyMCE (full-page mode) and pick whichever round-trips email HTML without stripping or rewriting it. If switching modes would change the HTML, warn and show a diff before applying.
  - Preview with a real subscriber's data, using the same Go renderer as sending.

## Security and operations
- Admin login with TOTP 2FA, secure sessions (hashed tokens), CSRF protection, Argon2id password hashing. Rate limits on public endpoints (subscribe, unsubscribe, tracking).
- Config via env vars and flags; AWS credentials through the standard SDK chain. Include the minimal IAM policy: `ses:SendEmail`, `ses:SendRawEmail`, `ses:GetAccount`, `ses:ListSuppressedDestinations`, configuration set management, and SQS receive/delete.
- Structured logging with `slog`, a Prometheus `/metrics` endpoint (configured vs achieved send rate, in-flight requests, SES latency, errors by type, claimer backlog, writer batch size and latency, dropped tracking events), `/healthz`.
- Online backups via `VACUUM INTO` on a schedule, documented Litestream compatibility.
- Use Go's standard `net/http` routing (1.22+ patterns), `sqlc` or hand-written SQL (no heavy ORM), embedded migrations.

## Testing and proof of speed
- A fake SES v2 server for tests that injects latency (configurable distribution), random `MessageRejected`, 429 throttling, 5xx errors and timeouts.
- A load test that sends a 350k-recipient campaign at 175/sec (and at 1,000/sec) against the fake server, with a realistic 60–100KB template. Report the achieved rate, p50/p99 request latency, memory, CPU and SQLite writer latency. The sender must stay within 2% of the configured rate without ever exceeding it.
- Benchmarks for rendering and MIME building, and for the results writer.
- Tests for:
  - the token bucket and backoff
  - crash/resume (kill the process mid-send and verify no duplicates under the default policy)
  - the claimed→unknown recovery
  - skipping recipients who unsubscribe mid-send
  - merge tags and escaping
  - MIME correctness (parse the output back with `net/mail` / `mime/multipart`)
  - one-click unsubscribe
  - SNS signature verification and deduplication
  - bounce/complaint suppression
  - the Sendy importer and the API
  - the `EXPLAIN QUERY PLAN` index test

## Deliverables and order of work
1. Architecture doc: package layout, final DB schema with index rationale, sending pipeline diagram.
2. Renderer, MIME builder and sending engine, plus fake SES and load test, proven before any UI work.
3. Subscribers, lists, import, suppression, SNS/SQS ingestion, job runner.
4. REST API plus OpenAPI.
5. Frontend: overview, campaigns, editor, reports, lists, settings.
6. Sendy importer.
7. README with AWS setup (IAM policy, configuration set, SNS/SQS wiring) and deployment as a systemd service.

Ask me before making decisions that change these constraints.