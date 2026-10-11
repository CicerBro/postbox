# Moving from Sendy

Import your existing audience and historical campaigns, review the result, then enable sending. Keep the original source so every decision can be checked after cutover.

## Prepare the cutover

Back up the destination Postbox database and secret, and retain a backup of Sendy. Stop the Postbox service before a production import so already running sends cannot continue during migration.

Choose one source: a MySQL database accessed with a SELECT-only account, or a UTF-8 SQL dump. Supply database credentials through a protected environment, with TLS configured in the MySQL DSN. The source database is only migration input, not a runtime dependency, and the importer does not print the DSN. Dump statements are parsed as data and never executed against SQLite.

Choose a stable source ID for this Sendy installation. Reuse it for previews and retries. It ties source records to their destination identities and prevents a rerun from becoming a second import.

## Create a SQL dump

Export the **entire Sendy database**, including table definitions and all rows. A subscriber CSV or a dump of only `subscribers` is insufficient: the importer also needs brands (`apps`) and lists, and uses templates, campaigns, custom fields and suppression tables when present. Keeping every table also lets it archive source data that has no direct mapping.

Run one of the commands below on the database host. Replace `sendy` with the database name from your Sendy configuration, and `sendy_reader` with an account that can read all its tables. Adjust the host and port if needed. `/safe` should be an existing private directory. `--password` prompts for the password without putting it in the command or shell history.

For **MariaDB**, use `mariadb-dump`:

```sh
umask 077
mariadb-dump \
  --host=127.0.0.1 --port=3306 --user=sendy_reader --password \
  --single-transaction --quick \
  --default-character-set=utf8mb4 --complete-insert --hex-blob \
  --no-tablespaces --skip-triggers --skip-routines --skip-events \
  sendy > /safe/sendy.sql
```

For **MySQL 8.x**, use `mysqldump`:

```sh
umask 077
mysqldump \
  --host=127.0.0.1 --port=3306 --user=sendy_reader --password \
  --single-transaction --quick \
  --default-character-set=utf8mb4 --complete-insert --hex-blob \
  --no-tablespaces --skip-triggers --skip-routines --skip-events \
  --set-gtid-purged=OFF --column-statistics=0 \
  sendy > /safe/sendy.sql
```

With a MySQL 5.7 client, omit `--column-statistics=0`. Use the MariaDB command for MariaDB clients, including older installations where that tool is named `mysqldump`; the MySQL-specific options are not interchangeable.

These commands keep schema and data, emit ordinary `INSERT ... VALUES` with explicit column names, and stream large tables. Multi-row inserts are supported. Triggers, stored routines, scheduled database events, tablespace definitions and replication state are not needed by the Postbox importer. See the [MariaDB dump options](https://mariadb.com/docs/server/clients-and-utilities/backup-restore-and-import-clients/mariadb-dump) and [MySQL dump options](https://dev.mysql.com/doc/refman/8.4/en/mysqldump.html) for client details.

For the final cutover, pause Sendy campaigns, cron jobs and signup/profile writes before exporting. `--single-transaction` gives a consistent snapshot for InnoDB tables. If any tables use MyISAM or another nontransactional engine, keep all writes stopped throughout the dump or replace that option with `--lock-all-tables` using an account with the required locking privileges. Avoid schema changes during the export.

Wait for the command to finish successfully before importing. Do not use `--force`, table filters, `--no-data`, `--no-create-info`, `--replace` or `--tab`. Keep the output as an uncompressed `.sql` file, transfer it privately to the Postbox host, and use its path in `POSTBOX_SENDY_DUMP` below. Keep your original Sendy backup separately, including any database objects needed to restore Sendy itself.

## Preview, then import

Run a preview against the intended source:

```sh
POSTBOX_SENDY_SOURCE_ID=sendy-production \
POSTBOX_SENDY_DUMP=/safe/sendy.sql \
POSTBOX_SENDY_DRY_RUN=true \
  postbox migrate-sendy --db /var/lib/postbox/postbox.db
```

The JSON report includes row counts, statuses, suppressions and warnings. A preview maps data in a temporary database without committing imported records. Opening the configured destination can still initialise its schema and sending safety switch. Preview validates source consistency; it does not predict collisions with contacts already in the destination.

Review every warning and compare counts with Sendy. When satisfied, run the same command without the dry-run variable:

```sh
POSTBOX_SENDY_SOURCE_ID=sendy-production \
POSTBOX_SENDY_DUMP=/safe/sendy.sql \
POSTBOX_SENDY_DRY_RUN=false \
  postbox migrate-sendy --db /var/lib/postbox/postbox.db
```

For a live MySQL source, set `POSTBOX_SENDY_DSN` instead of `POSTBOX_SENDY_DUMP`. Exactly one must be nonempty. Budget temporary disk for decoded source data and the preview database.

The SQL dump import reads local files and writes the Postbox database. It does not call external APIs, send confirmation or welcome emails, or enqueue outbound webhooks. A live MySQL source connects only to the configured source database. Keep the Postbox service stopped during cutover; the migration command does not run background workers.

## Review what arrived

| Source data | Result in Postbox |
| --- | --- |
| Brands, lists and templates | Imported with stable source identities |
| Duplicate addresses | Merged within a brand; conflicts are reported |
| Unsubscribed or unconfirmed memberships | Kept in their non-sendable membership states |
| Bounced, complained or blocklisted addresses | Kept suppressed globally |
| `blocked_domains` | Imported as domain suppressions within the referenced brand; NULL or zero `app` means global |
| Sent campaigns | Historical campaigns with supported aggregate counters |
| Drafts and scheduled campaigns | Cancelled historical records with a warning; no send jobs created |
| Unknown fields or report tables | Archived with warnings, subject to the adapter's safety checks |

Check sender identities, custom fields, timestamps, membership states and historical reports. Aggregate reports cannot supply missing recipient-level events or SES message IDs. Legacy dates without an offset are treated as UTC, so compare them with your previous installation's timezone.

An actual import turns sending off and leaves it off, including after failure. Restart the service when the import is complete, review the [first-send checks](sending-newsletters.md#send-or-schedule), then explicitly enable sending in Settings.

## Retry safely

Imports commit in batches. After a failure, fix the reported source problem and rerun with the same source ID. Committed batches remain; reruns refresh archives and apply newly observed restrictions without clearing an unsubscribe or suppression.

Postbox edits to mapped brands, lists, templates and campaigns remain authoritative. Correct historical content explicitly rather than giving the same Sendy installation a new source ID.

The adapter expects known table and column layouts and rejects unsupported data instead of guessing. Use the reference below to check compatibility with your Sendy version before cutover.

## Sendy adapter reference

The importer reads a Sendy MySQL database in a read-only, repeatable-read transaction, or streams a MySQL SQL dump. Dump SQL is never executed. CREATE TABLE statements provide column order; INSERT INTO ... VALUES tuples are decoded into data and bound to prepared SQLite statements. Extended multi-row inserts are consumed one tuple at a time, so a dump does not need to fit in memory.

Imports use prepared statements in immediate transactions, normally in batches of 1,000 rows and at most 10,000. Private temporary files spool source data in dependency order and are removed on completion or error. ANALYZE runs after success.

### Library entry points and reports

The Go entry point is:

```go
report, err := migrate.Run(ctx, db, migrate.Options{
    DumpPath: "sendy.sql", // alternatively DSN; exactly one is required
    SourceID: "production-sendy", // stable across previews and reruns
    DryRun: true,
    BatchSize: 1000,
})
```

`Import(ctx, db, source, options)` also accepts `DumpSource{Reader: reader}`, `MySQLSource{DSN: dsn}`, or any streaming `Source`. `Report` is JSON-ready and contains source row counts, processed imported row counts, membership/global status counts, deduplicated addresses, suppression row counts, and explicit warnings. Counts are source-row counts, not unique destination counts. On failure, imported counts reflect committed batches only. A library dry run executes the entire mapping against a private temporary SQLite database, without touching the destination or its sending switch. It validates source consistency, but does not predict collisions with existing destination subscribers.

### Assumed source layout

Table names are unprefixed. The current adapter expects `apps`, `lists`, and `subscribers` tables and requires at least one brand in `apps`. Empty lists or subscriber sets are valid. Source variants need explicit mapping updates; table/column names are not guessed silently.

| Sendy source | Destination | Supported source columns/aliases |
| --- | --- | --- |
| `apps` | `brands` | `id`, `app_name`/`name`, `from_name`, `from_email`/`email`, `reply_to` |
| `lists` | `lists` | `id`, `app`/`app_id`/`brand_id`, `name`, `opt_in`/`double_opt_in`, `custom_fields` |
| `custom_fields` or `customfields` | `custom_fields` | `id`, brand/list reference, `key`/`name`/`label`, `type` |
| `subscribers` | subscribers and memberships | `id`, `list`/`list_id`, `email`, `name`, `confirmed`, `unconfirmed`, `unsubscribed`, `bounced`, `complaint`/`complained`, `blocklisted`, `suppressed`, `status`, `custom_fields`/`fields` |
| `templates` | `templates` | `id`, brand reference, `name`/`title`, `html`/`html_text` |
| `campaigns` | archived `campaigns` and stats | `id`, brand reference, `subject`/`title`, `html_text`/`html`, `plain_text`/`text`, `sent`/`status`, numeric report counters |
| `suppressions`, `suppression_list`, `suppression_lists` | global suppressions | `id`, `email`/`domain`/`value`, optional `kind`, `reason` |
| `blocked_domains` | domain suppressions | `id`, `domain`, optional `app` for brand scope; `timestamp` and `block_attempts` retained in the source archive |

The source adapter is deliberately explicit because Sendy versions and installations vary. Validate the layout with your version's schema before production use. All complete source rows, including NULLs and unknown columns/tables, are stored in `migration_records`. Unknown columns and tables emit named warnings. Unknown-table rows without an `id` are archived by table-relative row ordinal with a warning; their original source offers no stable identity, so row assignments can change on a later source scan. An unknown table whose name indicates suppressions/blocklists is rejected, rather than allowing its addresses to become sendable. Missing references, invalid email addresses, invalid UTF-8, malformed custom fields, and unsupported INSERT expressions fail with actionable errors. Already committed batches remain; rerun with the same SourceID after fixing the issue.

Custom-field definitions use the expected `Field:Type%s%Other:Type` list representation. Values use matching `%s%` positions; a count mismatch fails. Subscriber fields may alternatively be a JSON object of string values. Unknown field types are retained as text with a warning. The delimiter format is an adapter assumption that must be checked against the installed Sendy version. [Sendy's export discussion](https://sendy.co/forum/discussion/891/custom-fields-for-exports) documents `%s%` as a value separator.

Timestamps accept Unix seconds, Unix milliseconds, RFC3339, and UTC `YYYY-MM-DD HH:MM:SS`/`YYYY-MM-DD`. Missing values use import time; an unrecognized date warns and retains the original value in the archive. Legacy Sendy dates without an offset are treated as UTC, so verify historical timestamps if the installation used a local timezone.

### Consent and suppression

Emails are trimmed, lowercased, and their domains punycoded through the same normalizer used by sending. Deduplication is per brand. Original source subscriber IDs all map to the merged subscriber in `migration_identities`, keyed by SourceID/entity/source ID. Generated list/campaign UUIDs are deterministic for that source.

Blocked domains are normalized and imported before subscribers. A nonzero `blocked_domains.app` must reference an imported brand. A NULL or zero app creates a global domain suppression. Matching addresses are excluded from sending, and existing pending recipients in the applicable scope are skipped. Repeat imports retain existing restrictions and apply newly observed blocks without creating duplicate suppressions.

List status precedence is unsubscribed, then unconfirmed, then subscribed. Unsubscribed/unconfirmed memberships remain membership states and do not create global suppression. A subscriber who remains subscribed in a different list may still receive that list's campaigns, as in Sendy's list-scoped unsubscribe setting. Rows with no confirmed or status column remain unconfirmed with a warning. Bounced, complained, blocklisted, and explicitly suppressed addresses create global suppressions, even if another brand/list has an active record. Nonzero bounce/complaint flags are interpreted conservatively. Unknown non-active status values remain blocklisted with a warning. Sending filters inactive subscribers and global email/domain suppressions. [Sendy's discussion of status scope](https://sendy.co/forum/discussion/1567/are-bounces-complaints-and-unsubscribes-global) explains its brand/list behavior; this importer keeps bounce/complaint suppression global for send safety.

Conflicting duplicate names and custom fields keep existing destination values, with a deduplication warning; every original row remains archived. Reruns refresh source archives and apply newly observed safety restrictions without reactivating an unsubscribe or removing suppression. Existing mapped brands/lists/templates/campaigns remain authoritative, so rerunning does not overwrite edits made in Postbox. To replace migrated historical content, make an explicit correction rather than inventing a new SourceID for the same installation.

### Historical data and limits

HTML and Sendy merge tags are preserved, including styles, table layouts, and conditional comments. A `sent` source campaign becomes a historical sent campaign. Every other source campaign becomes cancelled with a warning, preventing an old draft/schedule from entering Postbox's send queue. No imported campaign receives pending recipients or jobs.

Known numeric counters are imported from recipients/sent_count/sent_to/to_send, opens/opens_total, unique_opens/opens_unique, clicks/clicks_total, unique_clicks/clicks_unique, unsubscribes, bounces, and complaints. Sendy's comma-separated `subscriber_id:country` opens and `links.clicks` subscriber-ID sequences are also supported. Repeated entries count toward totals; unique opens use source subscriber IDs, and campaign unique clicks deduplicate IDs across all links. Source ID zero is retained as one anonymous identity. Historical link reports and opens by country are restored without creating recipient events or inventing timestamps. Human/machine classifications and recipient-level SES message identities cannot be inferred from these records; those remain unavailable rather than fabricated. Unknown report/event tables retain their complete source rows in the archive and emit warnings.

The dump reader supports quoted/backtick identifiers, qualified table names, SQL NULL, numeric values, UTF-8 quoted strings with MySQL escapes, doubled quotes, hex literals, CREATE column order, and INSERT/INSERT IGNORE with explicit or implicit columns. Comments and other statements, including DROP/SET/TRIGGER, are ignored and never executed. SQL expressions, INSERT SELECT, REPLACE, LOAD DATA, executable-comment inserts, binary non-UTF-8 data, and `NO_BACKSLASH_ESCAPES` dump mode are rejected. Export ordinary UTF-8 INSERT VALUES statements. The parser uses standard MySQL backslash escaping; ensure SQL mode matches before import. The adapter currently has fixture coverage and a 100,000-tuple streaming parser test; validate a dry run of the actual Sendy database before cutover.
