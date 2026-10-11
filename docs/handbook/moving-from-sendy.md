# Moving from Sendy

Import your existing audience and historical campaigns, review the result, then enable sending. Keep the original source so every decision can be checked after cutover.

## Prepare the cutover

Back up the destination Postbox database and secret, and retain a backup of Sendy. Stop the Postbox service before a production import so already running sends cannot continue during migration.

Choose one source: a MySQL database accessed with a SELECT-only account, or a UTF-8 SQL dump. Supply database credentials through a protected environment. Dump statements are parsed as data and never executed against SQLite.

Choose a stable source ID for this Sendy installation. Reuse it for previews and retries. It ties source records to their destination identities and prevents a rerun from becoming a second import.

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

## Review what arrived

| Source data | Result in Postbox |
| --- | --- |
| Brands, lists and templates | Imported with stable source identities |
| Duplicate addresses | Merged within a brand; conflicts are reported |
| Unsubscribed or unconfirmed memberships | Kept in their non-sendable membership states |
| Bounced, complained or blocklisted addresses | Kept suppressed; importer suppressions are global |
| Sent campaigns | Historical campaigns with supported aggregate counters |
| Drafts and scheduled campaigns | Cancelled historical records with a warning; no send jobs created |
| Unknown fields or report tables | Archived with warnings, subject to the adapter's safety checks |

Check sender identities, custom fields, timestamps, membership states and historical reports. Aggregate reports cannot supply missing recipient-level events or SES message IDs. Legacy dates without an offset are treated as UTC, so compare them with your previous installation's timezone.

An actual import turns sending off and leaves it off, including after failure. Restart the service when the import is complete, review the [first-send checks](sending-newsletters.md#send-or-schedule), then explicitly enable sending in Settings.

## Retry safely

Imports commit in batches. After a failure, fix the reported source problem and rerun with the same source ID. Committed batches remain; reruns refresh archives and apply newly observed restrictions without clearing an unsubscribe or suppression.

Postbox edits to mapped brands, lists, templates and campaigns remain authoritative. Correct historical content explicitly rather than giving the same Sendy installation a new source ID.

The adapter expects known table and column layouts and rejects unsupported data instead of guessing. Use the reference below to check compatibility with your Sendy version before cutover.

<!-- reference:migration -->
