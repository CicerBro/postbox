# Sendy migration

The importer reads a Sendy MySQL database in a read-only, repeatable-read transaction, or streams a MySQL SQL dump. Dump SQL is never executed. CREATE TABLE statements provide column order; INSERT INTO ... VALUES tuples are decoded into data and bound to prepared SQLite statements. Extended multi-row inserts are consumed one tuple at a time, so a dump does not need to fit in memory.

Before importing, back up the destination and retain the original Sendy database/dump. Use a dedicated SELECT-only MySQL account, with TLS configured in its DSN. The database is optional migration input, not a runtime dependency. Supply credentials via environment configuration rather than putting passwords in shell history. The importer does not print the DSN.

The Go entry point is:

```go
report, err := migrate.Run(ctx, db, migrate.Options{
    DumpPath: "sendy.sql", // alternatively DSN; exactly one is required
    SourceID: "production-sendy", // stable across previews and reruns
    DryRun: true,
    BatchSize: 1000,
})
```

`Import(ctx, db, source, options)` also accepts `DumpSource{Reader: reader}`, `MySQLSource{DSN: dsn}`, or any streaming `Source`. `Report` is JSON-ready and contains source row counts, processed imported row counts, membership/global status counts, deduplicated addresses, suppression row counts, and explicit warnings. Counts are source-row counts, not unique destination counts. On failure, imported counts reflect committed batches only. A dry run executes the entire mapping against a private temporary SQLite database, without touching the destination or its sending switch. It validates source consistency, but does not predict collisions with existing destination subscribers.

Run a dry run first and review all warnings. An actual import sets `sending_enabled=false` and leaves it disabled, including after failures. Review imported identities, statuses, and historical reports before explicitly enabling sending. Previously running in-flight sends may finish; stop the service before a production migration. An import writes batches of at most 10,000 rows in immediate transactions, normally 1,000, with prepared statements. Private temporary files spool source data in dependency order and are removed on completion/error. Budget temporary disk for the decoded source and, during preview, a temporary destination database. ANALYZE runs after success.

## Assumed source layout

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

The source adapter is deliberately explicit because Sendy versions and installations vary. Validate the layout with your version's schema before production use. All complete source rows, including NULLs and unknown columns/tables, are stored in `migration_records`. Unknown columns and tables emit named warnings. Unknown-table rows without an `id` are archived by table-relative row ordinal with a warning; their original source offers no stable identity, so row assignments can change on a later source scan. An unknown table whose name indicates suppressions/blocklists is rejected, rather than allowing its addresses to become sendable. Missing references, invalid email addresses, invalid UTF-8, malformed custom fields, and unsupported INSERT expressions fail with actionable errors. Already committed batches remain; rerun with the same SourceID after fixing the issue.

Custom-field definitions use the expected `Field:Type%s%Other:Type` list representation. Values use matching `%s%` positions; a count mismatch fails. Subscriber fields may alternatively be a JSON object of string values. Unknown field types are retained as text with a warning. The delimiter format is an adapter assumption that must be checked against the installed Sendy version. [Sendy's export discussion](https://sendy.co/forum/discussion/891/custom-fields-for-exports) documents `%s%` as a value separator.

Timestamps accept Unix seconds, Unix milliseconds, RFC3339, and UTC `YYYY-MM-DD HH:MM:SS`/`YYYY-MM-DD`. Missing values use import time; an unrecognized date warns and retains the original value in the archive. Legacy Sendy dates without an offset are treated as UTC, so verify historical timestamps if the installation used a local timezone.

## Consent and suppression

Emails are trimmed, lowercased, and their domains punycoded through the same normalizer used by sending. Deduplication is per brand. Original source subscriber IDs all map to the merged subscriber in `migration_identities`, keyed by SourceID/entity/source ID. Generated list/campaign UUIDs are deterministic for that source.

List status precedence is unsubscribed, then unconfirmed, then subscribed. Unsubscribed/unconfirmed memberships remain membership states and do not create global suppression. A subscriber who remains subscribed in a different list may still receive that list's campaigns, as in Sendy's list-scoped unsubscribe setting. Rows with no confirmed or status column remain unconfirmed with a warning. Bounced, complained, blocklisted, and explicitly suppressed addresses create global suppressions, even if another brand/list has an active record. Nonzero bounce/complaint flags are interpreted conservatively. Unknown non-active status values remain blocklisted with a warning. Sending filters inactive subscribers and global email/domain suppressions. [Sendy's discussion of status scope](https://sendy.co/forum/discussion/1567/are-bounces-complaints-and-unsubscribes-global) explains its brand/list behavior; this importer keeps bounce/complaint suppression global for send safety.

Conflicting duplicate names and custom fields keep existing destination values, with a deduplication warning; every original row remains archived. Reruns refresh source archives and apply newly observed safety restrictions without reactivating an unsubscribe or removing suppression. Existing mapped brands/lists/templates/campaigns remain authoritative, so rerunning does not overwrite edits made in Postbox. To replace migrated historical content, make an explicit correction rather than inventing a new SourceID for the same installation.

## Historical data and limits

HTML and Sendy merge tags are preserved, including styles, table layouts, and conditional comments. A `sent` source campaign becomes a historical sent campaign. Every other source campaign becomes cancelled with a warning, preventing an old draft/schedule from entering Postbox's send queue. No imported campaign receives pending recipients or jobs.

Known numeric counters are imported from recipients/sent_count/sent_to/to_send, opens/opens_total, unique_opens/opens_unique, clicks/clicks_total, unique_clicks/clicks_unique, unsubscribes, bounces, and complaints. Human/machine classifications and recipient-level SES message identities cannot be inferred from aggregate reports; those remain unavailable rather than fabricated. Unknown report/event tables retain their complete source rows in the archive and emit warnings. They are not rendered as Postbox recipient activity until a version-specific mapping is supplied. Past link URLs, IDs, and report representations remain recoverable from those records.

The dump reader supports quoted/backtick identifiers, qualified table names, SQL NULL, numeric values, UTF-8 quoted strings with MySQL escapes, doubled quotes, hex literals, CREATE column order, and INSERT/INSERT IGNORE with explicit or implicit columns. Comments and other statements, including DROP/SET/TRIGGER, are ignored and never executed. SQL expressions, INSERT SELECT, REPLACE, LOAD DATA, executable-comment inserts, binary non-UTF-8 data, and `NO_BACKSLASH_ESCAPES` dump mode are rejected. Export ordinary UTF-8 INSERT VALUES statements. The parser uses standard MySQL backslash escaping; ensure SQL mode matches before import. The adapter currently has fixture coverage and a 100,000-tuple streaming parser test; validate a dry run of the actual Sendy database before cutover.
