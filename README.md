# Postbox

Postbox is a self-hosted newsletter service built around SES v2 and SQLite. The Go binary embeds the React frontend and migrations. It uses no Redis, PHP, MySQL, Node or Bun at runtime. AWS SES and, when configured, SNS/SQS are the external delivery services. Sendy MySQL is needed only for a direct migration.

## Build and test

Install Go 1.27.2 or a newer supported security patch matching `go.mod`, and Bun 1.4.3. CI reads the Go version from `go.mod`; the Docker build uses Go 1.27.2. Build frontend assets before the binary:

```sh
make build
make test
make race
make security
make bench
make release
```

`make release` produces CGO-free Linux amd64 and arm64 binaries. Frontend source is in `web`; `bun install --frozen-lockfile` and `bun run build` are the supported frontend commands. Never use npm, yarn or pnpm.

Frontend API response types are generated from Go JSON response structs. After changing those structs, run `go run ./cmd/contractgen`; CI verifies the generated types are current with `go run ./cmd/contractgen -check` before building the frontend.

`make security` runs `govulncheck` v1.8.0 against Go source and its build toolchain, then `bun audit` against every frontend lockfile dependency, including development tools. CI runs the same checks and fails on reachable Go vulnerabilities, any frontend advisory, or a scanner request error. Both checks require network access and leave dependency files unchanged. Run them before a release and after dependency or toolchain updates. When upgrading Go, update `go.mod` and the Dockerfile together and rebuild deployed binaries to include standard-library fixes. Go 1.27.2 includes the October 8, 2026 [security fixes](https://go.dev/doc/devel/release#go1.27.2) for TLS, HTTP, textproto, templates, OS support and the Go command.

## First run

```sh
export POSTBOX_DB=/var/lib/postbox/postbox.db
export POSTBOX_BASE_URL=https://mail.example.com
export POSTBOX_AWS_REGION=eu-west-1
postbox init
# Supply secrets through a protected environment, not shell history.
export POSTBOX_ADMIN_EMAIL=admin@example.com
export POSTBOX_ADMIN_PASSWORD='<long unique password>'
export POSTBOX_ADMIN_TOTP='<base32 TOTP authenticator secret>'
postbox admin
unset POSTBOX_ADMIN_PASSWORD POSTBOX_ADMIN_TOTP
postbox serve
```

Sending starts disabled when a database is first opened through the Postbox CLI, including databases created by migration or development tools. This one-time safety initialization is atomic. Review verified domains, subscriber consent, target lists, the campaign checklist and SES quota, then enable the sending switch in Settings. The persisted switch survives subsequent initialization and restarts. The application never falls back to a fake sender when credentials are missing. Failed AWS calls remain errors and cannot be reported as successful deliveries.

Account passwords require at least 8 characters, including the initial administrator password supplied to `postbox admin` and passwords created or changed in Settings.

Configuration uses `POSTBOX_` environment variables and flags. Run `postbox serve --help` for all flags. Relevant defaults: `--db ./data/postbox.db`, `--listen 127.0.0.1:8080`, `--secure-cookies=true`, `--max-workers=512`, `--retention-days=90`, `--recovery-policy=unknown`, `--shutdown-timeout=60s`. Configure AWS access keys and an SES region for each brand in **Settings > Amazon SES**. Access keys and optional session tokens are encrypted in SQLite with a key derived from the server secret; the API never returns credential values. Brands without saved keys cannot send or check their SES connection.

The public base URL is required for service startup. Production requires HTTPS and secure cookies. Put a TLS reverse proxy in front of the loopback HTTP listener, preserve the original Host, and expose tracking paths, subscribe forms and `/events/sns`. Streaming campaign reports need proxy buffering disabled and generous idle timeouts. The server chooses cookie security from configuration, not untrusted forwarding headers. Local development can use `--development --base-url http://localhost:8080 --secure-cookies=false`.

The configured base URL is also the trust anchor for passkeys. Set `POSTBOX_BASE_URL` or `--base-url` to the exact browser origin, such as `https://mail.example.com` or `https://mail.example.com:8443`, with no path, query or fragment. Postbox derives the WebAuthn relying-party domain from that configured origin and checks browser request origins against it. It never derives either value from the request Host header. Passkeys require HTTPS, except that HTTP on `localhost` is allowed for development. IP addresses are not supported as relying-party domains. For local passkey testing, serve the embedded Go application at its configured localhost origin so the browser origin matches exactly.

The tracking HMAC key is generated once using cryptographic randomness at `DB.secret`, or `--secret-file`. Its permissions must be 0600. Back it up with the database. Changing or losing it invalidates links in already delivered messages and prevents decryption of saved brand AWS credentials. Preserve it across redeployments and restores.

## AWS setup

Open **Settings > Amazon SES** for the selected brand. Enter its IAM access key ID and secret access key, choose its SES region, and save. Use **Test connection** to read the AWS account ID, production/sandbox status, sending status, maximum messages per second, 24-hour sending quota, and rolling 24-hour usage. Temporary credentials also require a session token; renew them before expiry. Saved secrets stay blank in the form, and blank fields preserve their values.

Only administrator sessions can change AWS settings. Brand members can check their brand's account status. IAM access keys are different from SES SMTP credentials. Upgrading clears any legacy server-credential brand settings; enter keys for those brands before sending. The old server-wide SNS topic and SQS queue environment variables are no longer read; enter event resources for each brand.

SES is regional, so the picker selects a region rather than an availability zone. Credentials determine the AWS account. Send pacing is shared across brands using the same account and region, and quota refreshes use the selected connection. Campaigns, tests, and automated emails use the matching brand connection. Changing a connection clears only SES-imported suppressions and schedules a refresh; manual suppressions and subscriber bounce/complaint states remain.

Verify sending identities in the selected SES account and region with SES Easy DKIM, configure SPF/DMARC and request production access when the account is in the sandbox. SES quotas are fetched from `GetAccount` at runtime. The app sends raw MIME through SES v2 `SendEmail`, including explicit recipient destinations, configuration set and brand/campaign tags. It does not use SMTP or SES templates.

Replace `REGION`, `ACCOUNT` and `example.com` in [runtime IAM policy](deploy/iam-runtime.json) for each brand account. Connection checks need `ses:GetAccount` and `ses:ListEmailIdentities`; account suppression import needs `ses:ListSuppressedDestinations`. AWS STS `GetCallerIdentity` identifies the account for shared rate limiting and does not require an additional IAM allow statement. Restrict the SQS ARN to the brand's event queue. The policy includes `ses:SendRawEmail` permission for compatibility, although the implemented sender calls `ses:SendEmail`. The **Configure SES event destination** action uses the brand keys, which need the permissions in [setup IAM policy](deploy/iam-setup.json). Creating SNS/SQS infrastructure is a separate deployment responsibility.

Event ingestion is configured for each brand in **Settings > Amazon SES**. Enter the SNS topic ARN and, for queue delivery, the SQS queue URL. Postbox uses that brand's saved AWS keys to poll its queue. Changes take effect without restarting the server. Keep each topic and queue in the brand's SES region.

1. Create an SNS topic and an SQS queue for the brand, with a dead-letter queue. Configure a sensible redrive maximum (for example 5 receives), message retention and alarms for the dead-letter queue.
2. Allow SES to publish to SNS, scoped to your account and SES configuration set. Allow only that SNS topic to send to SQS, using the queue resource policy below.
3. Subscribe the queue ARN to the topic. Keep raw message delivery disabled so the queue contains SNS envelopes. SQS ingestion validates the configured topic and deletes a message only after its database transaction commits.
4. Enter the topic ARN and queue URL in that brand's Amazon SES settings. Set its SES configuration set in **Brand identity**, then select **Configure SES event destination** in the brand's Amazon SES settings. The brand key needs the permissions in [setup IAM policy](deploy/iam-setup.json) for this action.
5. Alternatively subscribe `https://mail.example.com/events/sns` as an HTTPS topic endpoint. The service verifies SNS signatures, the expected topic, and signing certificate URL/chain before processing or confirming a subscription. Permit outbound HTTPS access to AWS SNS signing certificates. Do not disable signature verification.

Queue resource policy example:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "sns.amazonaws.com"},
    "Action": "sqs:SendMessage",
    "Resource": "arn:aws:sqs:REGION:ACCOUNT:postbox-events",
    "Condition": {"ArnEquals": {"aws:SourceArn": "arn:aws:sns:REGION:ACCOUNT:postbox-events"}}
  }]
}
```

SNS topic permission example:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "ses.amazonaws.com"},
    "Action": "sns:Publish",
    "Resource": "arn:aws:sns:REGION:ACCOUNT:postbox-events",
    "Condition": {
      "StringEquals": {"AWS:SourceAccount": "ACCOUNT"},
      "ArnLike": {"AWS:SourceArn": "arn:aws:ses:REGION:ACCOUNT:configuration-set/postbox"}
    }
  }]
}
```

The SQS client uses 20-second long polling with a longer HTTP timeout. Poison messages remain available for redrive; monitor the dead-letter queue and inspect failures before replaying. Hard bounces and complaints suppress the address and skip its pending campaign recipients. The soft bounce threshold defaults to 3. Account-level suppression synchronization should be scheduled regularly through the durable job runner.

## Deployment and operations

### Docker Compose

The [Dockerfile](Dockerfile) builds the frontend with Bun and a static Go binary, then packages it in [`scratch`](https://docs.docker.com/build/building/base-images/#create-a-minimal-base-image-using-scratch) with CA certificates and embedded timezone data. The image runs as UID/GID `65532:65532`. The [Compose configuration](compose.yml) uses a read-only root filesystem, drops all capabilities, and persists SQLite, its WAL files, the tracking/credential-encryption secret, CSV jobs and backups in the `postbox-data` named volume.

Prepare runtime configuration and build the image:

```sh
cp .env.example .env
chmod 600 .env
# Edit .env: set POSTBOX_BASE_URL. Brand AWS keys are configured in the WebUI.
# POSTBOX_AWS_REGION is the default shown when configuring a new brand.
docker compose build --pull
docker compose run --rm postbox init
```

Create the initial administrator before starting the service. Supply `POSTBOX_ADMIN_EMAIL`, `POSTBOX_ADMIN_PASSWORD` and `POSTBOX_ADMIN_TOTP` through a protected shell environment, as described in First run, then forward them only to the one-off command:

```sh
docker compose run --rm \
  -e POSTBOX_ADMIN_EMAIL -e POSTBOX_ADMIN_PASSWORD -e POSTBOX_ADMIN_TOTP \
  postbox admin
unset POSTBOX_ADMIN_PASSWORD POSTBOX_ADMIN_TOTP
docker compose up -d
docker compose logs -f postbox
curl --fail http://127.0.0.1:8080/healthz
```

Configure a host TLS reverse proxy to forward your public origin to `127.0.0.1:8080` (or `POSTBOX_PORT`). Preserve Host, disable buffering for streaming reports, and restrict `/metrics`. A proxy running in another container should share the Compose network and connect to `postbox:8080`; loopback inside that proxy container refers to the proxy itself. Secure cookies and HTTPS validation are enforced by Compose. Monitor `/healthz` externally; the scratch image contains no shell or HTTP probe utility.

Brand sending and SQS polling use the keys configured for each brand in the WebUI. Server AWS credentials are not needed. Keep administrator bootstrap secrets out of `.env` so they are not passed to the long-running service.

One-off CLI commands share the same data volume. For example:

```sh
docker compose run --rm \
  -e POSTBOX_BACKUP_FILE=/var/lib/postbox/backups/manual-2026-10-10.sqlite \
  postbox backup
```

Use a new filename for each backup. Copy backups and `tracking.secret` off-host, and preserve the named volume across deployments. `docker compose down` keeps it; `docker compose down -v` deletes it. A bind mount requires a directory owned by `65532:65532` with mode `0700`. Run one Postbox service per database volume. Compose allows 75 seconds for graceful shutdown; keep this longer than the configured application shutdown timeout.

Rebuild with `docker compose build --pull` and deploy with `docker compose up -d`. BuildKit supports both Linux amd64 and arm64, for example `docker buildx build --platform linux/amd64,linux/arm64 --tag REGISTRY/postbox:TAG --push .`. Builder versions are pinned through `GO_VERSION` and `BUN_VERSION` build arguments; keep Go aligned with `go.mod` when upgrading.

### Systemd

Install `postbox` at `/usr/local/bin/postbox`, create a `postbox` system account, copy [environment example](deploy/postbox.env.example) to `/etc/postbox/postbox.env` with mode 0600, and install [systemd unit](deploy/postbox.service) at `/etc/systemd/system/postbox.service`. Initialize the database and create the admin as the service user, then enable the service:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now postbox
sudo journalctl -u postbox -f
```

The service logs structured JSON via `slog`. `/healthz` checks database availability; `/metrics` exports sending rates, backlog, in-flight requests, request latency, errors, writer batches, job outcomes and dropped tracking events in Prometheus format. Restrict metrics at the proxy or private network. Admin password login requires a TOTP code when TOTP is enabled. TOTP enrollment starts with password confirmation and completes only after verifying a code from the authenticator; removing it requires password confirmation and the current code. Changing either factor revokes the user's existing sessions.

Passkeys provide passwordless login with required WebAuthn user verification. The authenticator may verify the user with a device PIN or biometrics, depending on the device. Adding or removing a passkey requires the signed-in user to confirm their password and, when enabled, their existing TOTP code. Removing a passkey revokes all of that user's sessions and pending passkey ceremonies. Passkey login creates the same protected session with CSRF protection. API credentials are brand-scoped and hashed.

CSRF tokens remain stable for the lifetime of a session, including across browser tabs, reloads, and server restarts. Each password or passkey login creates a new session and token; logout, expiry, and session revocation invalidate writes using that session. The token is derived with SHA-256 and a domain prefix from the random 256-bit session cookie, and SQLite stores only the session and CSRF hashes. Migration `015_stable_session_csrf.sql` revokes pre-upgrade sessions once because their previous random CSRF tokens cannot be recovered from hashes. Users must sign in again after this upgrade.

On SIGTERM the app stops claiming campaign recipients, drains in-flight delivery and batched results, stops jobs and SQS polling, drains tracking writes and runs `PRAGMA optimize`. Allow at least 75 seconds in the supervisor. After a crash, claimed recipients become `unknown` and cannot be resent. SES event reconciliation can confirm their outcomes later.

Unsubscribe from all records a brand-wide marketing opt-out. It blocks list and segment campaigns at audience selection and before dispatch, including resumed campaigns. A one-list unsubscribe only changes that membership. Profile edits, imports and ordinary list additions cannot clear a brand opt-out. Explicit resubscription requires fresh consent and a list choice, and double opt-in lists remain ineligible until confirmation. Delivery suppressions still take precedence; confirmation email is a consent request, not a marketing campaign. Migration `014_brand_marketing_consent.sql` infers explicit historical unsubscribe-all events. Older events with empty details could mean one list or all lists, so it conservatively marks them opted out when no membership remains active and no later subscribe event exists. Review those records after upgrade and obtain fresh consent before restoring eligibility.

Production campaign delivery uses at-most-once dispatch whenever SES acceptance is uncertain. Explicitly requested test sends are the deliberate exception: they use a `[Test]` subject prefix and deduplicate normalized addresses within each test request. Timeouts, dropped connections, EOF, server faults and interrupted attempts stay `unknown` permanently until an SES event confirms acceptance. There is no manual resend or retry override for unknown recipients; `--recovery-policy=retry` and `POSTBOX_AMBIGUOUS_POLICY=retry` are rejected. Only definitive nonacceptance, such as throttling or failure to establish a connection, permits bounded retries. A campaign ledger keyed by a normalized email HMAC blocks a second potentially accepted dispatch across resume, competing workers, email changes and subscriber erasure/recreation. The ledger contains no recipient address and survives subscriber deletion; its separate secret belongs to the database and must stay with backups. WAL `synchronous=NORMAL` preserves the ledger across application-process crashes, but a host crash or power loss can lose a committed reservation and permit a duplicate. SES cannot guarantee exactly-once delivery, so this policy can omit an email when the first request actually failed but its outcome cannot be proven. See [sender performance and durability](docs/performance-profile.md).

Maintenance purges raw tracking/SES history in bounded batches, retains aggregates, refreshes reporting rollups, removes old unconfirmed subscriptions and schedules daily online backups. Tune raw retention using `POSTBOX_RETENTION_DAYS`; backups use `POSTBOX_BACKUP_DIR`.

Manual online backup:

```sh
POSTBOX_BACKUP_FILE=/safe/path/postbox-2026-10-10.sqlite postbox backup --db /var/lib/postbox/postbox.db
```

The output path must be new. Backups use SQLite `VACUUM INTO`, so they remain consistent while service reads and writes continue. Copy backups and the tracking secret off-host and periodically test restoring them. Litestream is optional infrastructure: it can replicate the WAL database, but is not required or bundled. Follow Litestream's SQLite/WAL restore guidance, stop Postbox before replacing a live database, and restore the matching tracking secret. Do not copy only a live `.db` file while ignoring its WAL.

## Sendy migration

Take a source backup first. Use a SELECT-only MySQL account or a SQL dump. Choose a stable source ID and reuse it on retries:

```sh
POSTBOX_SENDY_SOURCE_ID=sendy-production POSTBOX_SENDY_DUMP=/safe/sendy.sql POSTBOX_SENDY_DRY_RUN=true postbox migrate-sendy
POSTBOX_SENDY_SOURCE_ID=sendy-production POSTBOX_SENDY_DUMP=/safe/sendy.sql postbox migrate-sendy
# Alternatively provide POSTBOX_SENDY_DSN through a protected environment.
```

Migration reports imported records, source statuses, suppressions and warnings as JSON. Dry-run does not commit imported data. Suppressed, bounced, complained, unsubscribed and unconfirmed Sendy subscribers are preserved with their appropriate states. Review totals and status counts before enabling sending. SQL dumps are parsed as data and are never executed against SQLite. See [Sendy migration](docs/migration.md) for source mappings, rerun behavior and importer limits.

## Delivery proof

Use `go test ./...` and race checks for correctness, `make bench` for MIME/rendering performance, and `cmd/fakeses` plus `cmd/loadtest` for reproducible fake delivery runs. The fake sender is explicit tooling and is never selected by the production service. The load tool reports achieved rate, request latency, process resource use and SQLite writer latency. Run the full 350,000-recipient scenarios at both 175/sec and 1,000/sec before relying on a new deployment for a send day. A fast short test does not prove a 33-minute run. See [sender profiling and pacing](docs/performance-profile.md) for recorded full-run results, raw artifacts and their limitations.

Full fake-server scenarios run through the HTTP SES v2 adapter, without live credentials:

```sh
go run ./cmd/loadtest --recipients 350000 --rate 175 --message-bytes 100000 --output load-175.json
go run ./cmd/loadtest --recipients 350000 --rate 1000 --message-bytes 100000 --output load-1000.json
go run ./cmd/fakeses --listen 127.0.0.1:9001 --rate 175 --throttle-every 50 --server-error-every 100
```

Set `POSTBOX_NOTIFICATION_FROM` to a verified sender and `POSTBOX_NOTIFICATION_TO` to comma-separated admin addresses to receive campaign start/finish notifications. These durable email jobs share the account limiter and sending switch. Double opt-in confirmations and optional goodbye messages use the same transactional email queue. CSV job payload files are private basenames under the database directory’s `jobs` subdirectory; imports stream and exports create mode-0600 files.

All CSV downloads, including subscriber export jobs and campaign recipient exports, use spreadsheet-safe cells. Values whose first non-whitespace, non-control, non-format character is `=`, `+`, `-`, or `@` (including their fullwidth forms) receive a leading apostrophe. CSV quoting still preserves commas, quotes, line breaks, and Unicode. The database and JSON API retain the original values. These CSV downloads are not lossless machine backups: reimporting them retains the safety apostrophe as data, because an importer cannot distinguish an added escape from an original apostrophe. Use the JSON API or database backups when exact values must roundtrip; there is no raw CSV download mode.

The raw HTML editor is authoritative. Email templates remain full documents including their head, styles and MSO comments. Review WYSIWYG conversions as a source diff to check that the email markup is preserved.
