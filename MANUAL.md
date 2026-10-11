# Postbox manual

Deployment, administration and operational reference. See the [README](README.md) for the project overview and development commands.

- [First run and configuration](#first-run-and-configuration)
- [Environment variables and CLI flags](#environment-variables-and-cli-flags)
- [AWS and SES events](#aws-and-ses-events)
- [Deployment](#deployment)
- [Authentication and sessions](#authentication-and-sessions)
- [Reverse proxies and rate limits](#reverse-proxies-and-rate-limits)
- [Public subscriptions and consent](#public-subscriptions-and-consent)
- [Delivery and recovery](#delivery-and-recovery)
- [Maintenance, backup and restore](#maintenance-backup-and-restore)
- [Sendy migration](#sendy-migration)
- [Campaign workflows and CSV](#campaign-workflows-and-csv)
- [Capacity testing](#capacity-testing)

## First run and configuration

```bash
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

The public base URL is required for service startup. Production requires HTTPS and secure cookies. Put a TLS reverse proxy in front of the loopback HTTP listener, preserve the original Host, and expose tracking paths, subscribe forms and `/events/sns`. Streaming campaign reports need proxy buffering disabled and generous idle timeouts. The server chooses cookie security from configuration, not untrusted forwarding headers. Local development can use `--development --base-url http://localhost:8080 --secure-cookies=false`; these flags are for a local instance only.

The configured base URL is also the trust anchor for passkeys. Set `POSTBOX_BASE_URL` or `--base-url` to the exact browser origin, such as `https://mail.example.com` or `https://mail.example.com:8443`, with no path, query or fragment. Postbox derives the WebAuthn relying-party domain from that configured origin and checks browser request origins against it. It never derives either value from the request Host header. Passkeys require HTTPS, except that HTTP on `localhost` is allowed for development. IP addresses are not supported as relying-party domains. For local passkey testing, serve the embedded Go application at its configured localhost origin so the browser origin matches exactly.

The tracking HMAC key is generated once using cryptographic randomness at `DB.secret`, or `--secret-file`. Its permissions must be 0600. Back it up with the database. Changing or losing it invalidates links in already delivered messages and prevents decryption of saved brand AWS credentials. Preserve it across redeployments and restores.

## Environment variables and CLI flags

### Commands and precedence

The main binary accepts `postbox <command> [flags]`. Put flags after the command. `postbox help` lists commands; `postbox <command> --help` lists the shared flags. Single-dash and double-dash forms work, such as `-db` and `--db`. Use `--secure-cookies=false` to disable a boolean flag; a separate `false` argument is not a flag value.

| Command | Purpose | Additional required environment |
| --- | --- | --- |
| `serve` | Apply migrations, load the secret, start HTTP, sending, jobs, tracking and brand SQS polling. | A public origin through `POSTBOX_BASE_URL` or `--base-url`. |
| `init` | Apply migrations and create or validate the tracking/credential secret. | None. |
| `admin` | Create an administrator with password and TOTP. | `POSTBOX_ADMIN_EMAIL`, `POSTBOX_ADMIN_PASSWORD`, `POSTBOX_ADMIN_TOTP`. |
| `backup` | Create an online SQLite backup. | `POSTBOX_BACKUP_FILE`. |
| `migrate-sendy` | Import a Sendy database or dump and print a JSON report. | `POSTBOX_SENDY_SOURCE_ID` and exactly one of `POSTBOX_SENDY_DSN` or `POSTBOX_SENDY_DUMP`. |

Every command opens the configured database, applies pending migrations and initializes the sending safety switch if it has not previously been initialized. The same flags are parsed and validated for all commands, although most service options only affect `serve`. Only `serve` requires a valid public origin and checks production HTTPS/cookie requirements.

Explicit flags override the matching environment variable, which overrides the built-in default below. The native binary does not load `.env` files itself: export variables, use a supervisor environment file, or use Compose. Configuration is read when the process starts; restart after changing it. Persisted brand settings, sending controls and public subscription controls are managed through Settings or the API and take effect without an environment change.

String variables explicitly set to an empty string override their defaults. Empty boolean, integer and duration variables use their defaults. Environment booleans accept Go boolean forms (`true`, `false`, `1`, `0`, `t`, `f` and their supported capitalization); an invalid boolean environment value silently uses the default. Invalid integer or duration environment values become zero and fail positive-value validation unless overridden by a valid flag. Invalid flag values are errors. Durations use Go syntax, such as `30s`, `5m`, `24h` or `1h30m`; `1d` is not supported. Worker counts, retention days, bounce thresholds and both durations must be positive.

### Shared flags and matching environment variables

Defaults in this table are for the native binary. Container overrides are listed below.

| Flag | Environment variable | Default | Meaning |
| --- | --- | --- | --- |
| `--db` | `POSTBOX_DB` | `./data/postbox.db` | SQLite file to open or create. Relative paths resolve from the working directory. The default secret path and CSV job directory are derived from this path. |
| `--listen` | `POSTBOX_LISTEN` | `127.0.0.1:8080` | HTTP bind address and port. Use `0.0.0.0:8080` when the listener must be reachable outside its network namespace. This does not change the public URL. |
| `--base-url` | `POSTBOX_BASE_URL` | Empty | Public origin used for email links, tracking, hosted forms and passkey validation. Required for `serve`; use an absolute origin without credentials, a non-root path, query or fragment. HTTPS is required outside development mode. |
| `--region` | `POSTBOX_AWS_REGION` | `AWS_REGION`, otherwise empty | Default region offered for a brand without saved AWS settings. A brand's saved region controls its AWS requests. This is a region, such as `eu-west-1`, not an availability zone. An explicitly empty `POSTBOX_AWS_REGION` suppresses the `AWS_REGION` fallback. |
| `--secret-file` | `POSTBOX_SECRET_FILE` | Configured database path plus `.secret` | File holding the generated 32-byte base64 tracking/credential encryption secret. An empty value uses the derived path. Existing files must be regular files with no group/other access; generated files use mode 0600. Back it up with the database. |
| `--geoip` | `POSTBOX_GEOIP` | Empty | Optional local MaxMind database path for tracking geolocation. Empty disables GeoIP lookup. An unreadable or invalid configured database prevents `serve` from starting. |
| `--backup-dir` | `POSTBOX_BACKUP_DIR` | `./data/backups` | Directory for recurring online backups. An empty string disables recurring backups. It does not set the output path for the `backup` command. |
| `--trusted-proxies` | `POSTBOX_TRUSTED_PROXIES` | Empty, trust none | Comma-separated proxy IP addresses or CIDRs allowed to supply `X-Forwarded-For`. Whitespace around entries is accepted; invalid addresses and empty entries are rejected. See [proxy configuration](#reverse-proxies-and-rate-limits). |
| `--turnstile-site-key` | `POSTBOX_TURNSTILE_SITE_KEY` | Empty | Cloudflare Turnstile public site key displayed by hosted signup forms. Set it together with the matching secret when enabling Turnstile. |
| `--turnstile-secret` | `POSTBOX_TURNSTILE_SECRET` | Empty | Secret used to verify submitted Turnstile tokens. Empty disables server-side Turnstile verification. Supply it through a protected environment rather than command arguments. Environment-loaded secrets are hidden in flag help. |
| `--secure-cookies` | `POSTBOX_SECURE_COOKIES` | `true` | Set the Secure attribute on authentication cookies. Production requires `true`; local HTTP development normally uses `false`. |
| `--development` | `POSTBOX_DEVELOPMENT` | `false` | Allow an HTTP public origin and non-secure cookies for local development. It does not enable sending or select a fake AWS sender. |
| `--max-workers` | `POSTBOX_MAX_WORKERS` | `512` | Upper bound on campaign sender concurrency. Actual concurrency adapts to SES quota and measured latency; increasing this does not raise the account quota. |
| `--retention-days` | `POSTBOX_RETENTION_DAYS` | `90` | Number of days of raw tracking and SES history retained by housekeeping. Aggregate reports remain. Unconfirmed membership retention is separately fixed at 30 days. |
| `--soft-bounce-limit` | `POSTBOX_SOFT_BOUNCE_LIMIT` | `3` | Number of transient bounces after which an address is suppressed within the originating brand. Hard bounces and complaints do not wait for this threshold. |
| `--recovery-policy` | `POSTBOX_RECOVERY_POLICY` | `unknown` | Policy for interrupted or uncertain dispatches. Only `unknown` is supported. `retry` and every other value are rejected to avoid another potentially accepted send. |
| `--session-ttl` | `POSTBOX_SESSION_TTL` | `24h` | Lifetime assigned to newly created authentication sessions. Existing sessions retain their recorded expiry. |
| `--shutdown-timeout` | `POSTBOX_SHUTDOWN_TIMEOUT` | `60s` | Maximum graceful shutdown period for HTTP requests and background work. Give the process supervisor a longer stop grace period. |

`--help` and `-h` print flag help for a subcommand and exit without opening the database. They have no environment equivalent.

### Environment-only application options

These variables have no matching CLI flag. Unset variables are empty unless a default is shown.

| Variable | Command or feature | Meaning |
| --- | --- | --- |
| `POSTBOX_ADMIN_EMAIL` | `admin` | Required email address for the administrator to create. |
| `POSTBOX_ADMIN_PASSWORD` | `admin` | Required initial password, at least 8 characters. It is hashed before storage. Remove this variable from the environment after creating the account. |
| `POSTBOX_ADMIN_TOTP` | `admin` | Required base32 authenticator secret used to enable TOTP on the new administrator. Supply the secret, not a current six-digit code. Remove it after account creation. |
| `POSTBOX_BACKUP_FILE` | `backup` | Required new output filename for the manual SQLite backup. An existing file is rejected. The tracking secret is backed up separately. |
| `POSTBOX_NOTIFICATION_TO` | `serve`, campaign notifications | Comma-separated recipient addresses for campaign start/finish notifications. Empty disables notifications. Addresses are normalized when the notification job runs; invalid addresses cause job errors. |
| `POSTBOX_NOTIFICATION_FROM` | `serve`, campaign notifications | Verified sender address for notifications. Required at startup when `POSTBOX_NOTIFICATION_TO` is nonempty. Delivery uses the event's brand AWS connection, rate limiter and sending switch. |
| `POSTBOX_SENDY_SOURCE_ID` | `migrate-sendy` | Required stable identifier for the source installation. Reuse it on retries so source identities remain idempotent. |
| `POSTBOX_SENDY_DSN` | `migrate-sendy` | MySQL DSN, such as `reader:password@tcp(127.0.0.1:3306)/sendy`. Use a SELECT-only account. Exactly one of this variable and `POSTBOX_SENDY_DUMP` must be nonempty. |
| `POSTBOX_SENDY_DUMP` | `migrate-sendy` | Path to a Sendy SQL dump parsed as data. Exactly one of this variable and `POSTBOX_SENDY_DSN` must be nonempty. |
| `POSTBOX_SENDY_DRY_RUN` | `migrate-sendy` | Default `false`. A valid Go boolean enables validation/reporting without committing imported records. Invalid values are errors. Opening the target database can still initialize its schema and sending safety setting. |
| `POSTBOX_AMBIGUOUS_POLICY` | Legacy compatibility check | Empty or `unknown` is accepted. Any other value, including `retry`, fails configuration before flags are parsed. This is not a retry override and has no flag equivalent. Use `POSTBOX_RECOVERY_POLICY=unknown` for the current setting. |

There is no environment variable or CLI flag for the persisted sending switch or the application/brand public signup switches. Set them in the WebUI or API. Brand AWS access keys, optional session tokens, SES configuration sets, SNS topics and SQS queues are also configured per brand, not through main-binary flags.

### AWS environment compatibility

Postbox supplies the AWS SDK with each brand's saved region and static credentials. `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN`, shared credential files, IAM instance roles and profile credentials do not replace that brand configuration. Setting `AWS_ACCESS_KEY_ID=fake` in the shell alone does not configure a brand or activate fake sending.

The SDK still reads endpoint configuration when constructing brand SES v2, STS and SQS clients. The following controls are useful for local AWS emulation:

| Variable | Default | Meaning |
| --- | --- | --- |
| `AWS_REGION` | Empty | Fallback for the app's default brand region when neither `--region` nor `POSTBOX_AWS_REGION` provides one. Saved brand regions take precedence for requests. |
| `AWS_ENDPOINT_URL` | Normal AWS endpoints | SDK-wide endpoint override, such as `http://127.0.0.1:9001`. Applies to SES v2, STS and SQS unless a service-specific override takes precedence. |
| `AWS_ENDPOINT_URL_SESV2` | Unset | Endpoint override for SES v2 requests only. |
| `AWS_ENDPOINT_URL_STS` | Unset | Endpoint override for STS account identity checks only. A local emulator must support `GetCallerIdentity` for connection checks and sending. |
| `AWS_ENDPOINT_URL_SQS` | Unset | Endpoint override for brand SQS clients. |
| `AWS_IGNORE_CONFIGURED_ENDPOINT_URLS` | `false` | SDK boolean that ignores configured environment/shared-config endpoint URLs when enabled. |

Outbound HTTP clients also inherit the standard Go `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment settings through the default transport. `HTTP_PROXY` applies to HTTP requests, `HTTPS_PROXY` to HTTPS requests, and `NO_PROXY` lists hosts or networks that bypass the proxy. Other standard AWS SDK and Go runtime environment settings belong to those libraries, not to Postbox's application configuration. Endpoint overrides route real SDK requests; they do not swap in the in-process fake sender. For local QA, save fake credentials and a region for the test brand, start `cmd/fakeses`, and set `AWS_ENDPOINT_URL` on `postbox serve`. Do not use real brand keys against a test endpoint.

### Container, build and browser-test settings

| Setting | Type and default | Meaning |
| --- | --- | --- |
| `POSTBOX_PORT` | Compose interpolation variable, `8080` | Host loopback port published as `127.0.0.1:PORT:8080`. It is not read by the binary and does not change the container listener or public origin. |
| `GO_VERSION` | Docker build argument, `1.27.2` | Go builder image version. Override with `docker build --build-arg GO_VERSION=...`; keep it aligned with `go.mod`. |
| `BUN_VERSION` | Docker build argument, `1.4.3` | Bun frontend builder image version. Override with `docker build --build-arg BUN_VERSION=...`. |
| `TARGETOS`, `TARGETARCH` | BuildKit platform arguments | Target operating system and architecture for the Go build, normally supplied by `docker buildx --platform`. These are build inputs, not runtime settings. |
| `GOVULNCHECK_VERSION` | Make variable, `v1.8.0` | Version used by `make security-go`. Override as `make security-go GOVULNCHECK_VERSION=...`. |
| `CI` | Browser-test environment variable, unset | Any nonempty value prevents Playwright from reusing an existing Vite server. The browser suite starts its own server at `127.0.0.1:5174`. |

The image itself defaults to `POSTBOX_DB=/var/lib/postbox/postbox.db`, `POSTBOX_SECRET_FILE=/var/lib/postbox/tracking.secret`, `POSTBOX_BACKUP_DIR=/var/lib/postbox/backups` and `POSTBOX_LISTEN=0.0.0.0:8080`. Its working directory and `HOME` are `/var/lib/postbox`.

The supplied Compose file loads `.env` through `env_file`. Its explicit `environment` entries override same-named values in that file: it fixes the four container paths/listener above, `POSTBOX_SECURE_COOKIES=true`, `POSTBOX_DEVELOPMENT=false` and `POSTBOX_SHUTDOWN_TIMEOUT=60s`. It requires `POSTBOX_BASE_URL` for interpolation and defaults `POSTBOX_AWS_REGION` to `eu-west-1`. Other supported options in `.env`, such as retention, workers, notifications or trusted proxies, are passed through. To change a fixed entry, edit or override the Compose service. A shell variable can affect Compose interpolation without automatically becoming an additional container variable.

### Bundled development command flags

These tools use their own flag sets and do not load the main binary's `POSTBOX_*` configuration. Each accepts `--help` or `-h`. Their test data and fake AWS calls are for development and capacity measurements.

`go run ./cmd/fakeses [flags]` starts a local SES/STS emulator:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--listen` | `:9001` | Emulator HTTP bind address. Use `127.0.0.1:9001` for a listener limited to the local host. |
| `--rate` | `175` | Advertised account maximum send rate in messages per second. |
| `--latency` | `100ms` | Base simulated request latency. |
| `--jitter` | `50ms` | Uniform additional simulated latency. |
| `--reject-every` | `0` | Reject each Nth send. Zero disables this fault. |
| `--throttle-every` | `0` | Throttle each Nth send. Zero disables this fault. |
| `--server-error-every` | `0` | Return HTTP 500 for each Nth send. Zero disables this fault. |
| `--timeout-every` | `0` | Accept each Nth send, then delay its response to simulate an uncertain outcome. Zero disables this fault. |

`go run ./cmd/loadtest [flags]` seeds synthetic campaign data and runs delivery through its own local fake SES server. Use a dedicated test database; it does not require live AWS credentials or `cmd/fakeses` to be running.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--recipients` | `350000` | Number of synthetic recipients; must be positive. |
| `--rate` | `175` | Simulated SES account quota in messages per second; must be positive. |
| `--message-bytes` | `100000` | Approximate HTML template size in bytes. |
| `--latency` | `100ms` | Base simulated SES request latency. |
| `--jitter` | `50ms` | Uniform additional simulated latency. |
| `--db` | New temporary SQLite file | Database path for the generated test data. |
| `--output` | Empty | Optional JSON report output path. Without it, the report is printed only to stdout. |
| `--workers` | `1024` | Sender concurrency cap for this load run. |
| `--cpu-profile` | Empty | Optional output path for a send-phase Go CPU profile. |
| `--heap-profile` | Empty | Optional output path for a live heap profile after completion. |
| `--background-write-interval` | `0` | Interval between concurrent settings commits to simulate writer contention, such as `10ms`. Zero disables these writes. |

`go run ./cmd/contractgen --check` verifies generated frontend contracts against Go API types and exits with an error if they differ. Its only application flag is `--check`, default `false`; without it the generator writes updated contracts.

## AWS and SES events

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

The SQS client uses 20-second long polling with a longer HTTP timeout. Poison messages remain available for redrive; monitor the dead-letter queue and inspect failures before replaying. Hard bounces and complaints suppress the address within the originating brand and skip that brand's pending campaign recipients. A complaint from one brand does not change subscribers in another brand. Explicit administrator global suppressions remain global. The soft bounce threshold defaults to 3. Account-level suppression synchronization runs through the durable job runner. SES ingestion must resolve an originating brand from its configured topic/queue, event tags or an unambiguous stored message ID; events that cannot be scoped are rejected.

On upgrade, historical global suppressions and subscriber states are retained conservatively because their original source and later consent cannot reliably be reconstructed. Review global SES suppressions and contacts affected by earlier processing. Obtain explicit fresh consent before restoring marketing eligibility; removing a suppression alone does not establish consent.

## Deployment

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
  -e POSTBOX_BACKUP_FILE=/var/lib/postbox/backups/manual-YYYY-MM-DD.sqlite \
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

### Monitoring and shutdown

The service logs structured JSON via `slog`. `/healthz` checks database availability; `/metrics` exports sending rates, backlog, in-flight requests, request latency, errors, writer batches, job outcomes and dropped tracking events in Prometheus format. Restrict metrics at the proxy or private network.

On SIGTERM the app stops claiming campaign recipients, drains in-flight delivery and batched results, stops jobs and SQS polling, drains tracking writes and runs `PRAGMA optimize`. Allow at least 75 seconds in the supervisor. After a crash, claimed recipients become `unknown` and cannot be resent. SES event reconciliation can confirm their outcomes later.

## Authentication and sessions

Admin password login requires a TOTP code when TOTP is enabled. TOTP enrollment starts with password confirmation and completes only after verifying a code from the authenticator; removing it requires password confirmation and the current code. Changing either factor revokes the user's existing sessions.

Passkeys provide passwordless login with required WebAuthn user verification. The authenticator may verify the user with a device PIN or biometrics, depending on the device. Adding or removing a passkey requires the signed-in user to confirm their password and, when enabled, their existing TOTP code. Removing a passkey revokes all of that user's sessions and pending passkey ceremonies. Passkey login creates the same protected session with CSRF protection. API credentials are brand-scoped and hashed.

Campaign report streams recheck the active session or API credential and campaign access before each report update. Logout, expiry, credential revocation or loss of brand access closes the stream.

CSRF tokens remain stable for the lifetime of a session, including across browser tabs, reloads, and server restarts. Each password or passkey login creates a new session and token; logout, expiry, and session revocation invalidate writes using that session. The token is derived with SHA-256 and a domain prefix from the random 256-bit session cookie, and SQLite stores only the session and CSRF hashes. Migration `015_stable_session_csrf.sql` revokes pre-upgrade sessions once because their previous random CSRF tokens cannot be recovered from hashes. Users must sign in again after this upgrade.

## Reverse proxies and rate limits

Public password login, passkey login, subscription forms and confirmation links
use the canonical client IP address as their rate limit identity. Source ports
never form part of an identity. IPv4 mapped IPv6 addresses share the IPv4 bucket.
Authenticated TOTP enrollment, verification, removal and administrator resets,
plus passkey registration and revocation, use the acting user account ID.
Opening new connections or sessions does not reset these account limits.

The default trusts no reverse proxies. All forwarding headers are ignored unless
the direct socket peer matches an explicitly configured trusted proxy IP or CIDR.
Set `POSTBOX_TRUSTED_PROXIES` or `--trusted-proxies` to a comma separated list,
for example `127.0.0.1,::1` for a same-host proxy or `10.20.0.2/32` for a dedicated
proxy address. Invalid entries fail startup. Container deployments must configure
the actual socket peer as observed inside the container, which can be a bridge
or gateway IP instead of the host loopback address.

Configure every trusted proxy to overwrite incoming `X-Forwarded-For` on the
first ingress hop, or append the actual peer address when preserving a chain.
Only list proxies you control, and restrict direct access to the application.
Avoid trusting broad networks containing client devices. Headers are interpreted
from right to left, skipping trusted proxy hops and stopping at the nearest
untrusted IP. An earlier client supplied address, valid or malformed, cannot
override that hop.
`Forwarded` and `X-Real-IP` are ignored. Forwarded entries must be bare IPv4 or
IPv6 addresses without ports or zones. Malformed addresses within the inspected
trusted suffix, more than 32 hops, or more than 4096 bytes fall back to the
direct peer identity. Malformed direct peer
addresses use one shared `unknown` bucket.

Each process applies fixed one minute windows: password login and authenticated
factor checks allow 10 requests, passkey operations and subscription forms allow
20, confirmation links allow 30, and authenticated API keys allow 600. Limits
are in memory per process. Multiple processes require a shared external limiter
if a deployment needs aggregate limits. Expired buckets are removed during
requests, including rejected requests. At most 10,000 active identities are
retained; requests for new identities are denied while capacity is full, without
evicting existing throttles.

## Public subscriptions and consent

Open **Settings > Public subscriptions** to control anonymous signup. The application and selected brand switches must both be on; both default to on for existing installations and new brands. The application switch is administrator-only. Changing a brand switch requires write access to that brand; read access allows viewing it. Brand API keys follow the same permissions and cannot change the application switch. Disabling either switch makes the hosted signup form and its submission return HTTP 403 and prevents generating a new embed form. Existing confirmation and unsubscribe links remain usable.

The settings are persisted in SQLite and take effect immediately. Administrators can read or patch `/api/v1/application-settings`; the brand-scoped endpoint is `/api/v1/subscription-settings?brand_id=ID`. Both accept `{ "public_subscriptions_enabled": true }` or `false` in a PATCH. The brand response includes `application_enabled` and the effective `enabled` state. These are saved application settings, not environment flags.

Anonymous signup preserves an existing subscriber's name, custom fields and delivery state. Submitted profile changes are not applied by confirmation. Original consent metadata is retained unless a prior opt-out is explicitly renewed with fresh consent. Use the authenticated subscriber editor to change existing profiles.

Unsubscribe from all records a brand-wide marketing opt-out. It blocks list and segment campaigns at audience selection and before dispatch, including resumed campaigns. A one-list unsubscribe only changes that membership. Profile edits, imports and ordinary list additions cannot clear a brand opt-out. Explicit resubscription requires fresh consent and a list choice, and double opt-in lists remain ineligible until confirmation. Delivery suppressions still take precedence; confirmation email is a consent request, not a marketing campaign. Migration `014_brand_marketing_consent.sql` infers explicit historical unsubscribe-all events. Older events with empty details could mean one list or all lists, so it conservatively marks them opted out when no membership remains active and no later subscribe event exists. Review those records after upgrade and obtain fresh consent before restoring eligibility.

## Delivery and recovery

Production campaign delivery uses at-most-once dispatch whenever SES acceptance is uncertain. Explicitly requested test sends are the deliberate exception: they use a `[Test]` subject prefix and deduplicate normalized addresses within each test request. Timeouts, dropped connections, EOF, server faults and interrupted attempts stay `unknown` until a trusted SES event confirms acceptance. Reconciliation saves the SES message ID, updates the recipient, send attempt and dispatch ledger, and corrects outcome counts and original dispatch-hour counters in the same transaction. Repeated events do not inflate acceptance counts or permit another send. There is no manual resend or retry override for unknown recipients; `--recovery-policy=retry` and `POSTBOX_AMBIGUOUS_POLICY=retry` are rejected. Only definitive nonacceptance, such as throttling or failure to establish a connection, permits bounded retries. A campaign ledger keyed by a normalized email HMAC blocks a second potentially accepted dispatch across resume, competing workers, email changes and subscriber erasure/recreation. The ledger contains no recipient address and survives subscriber deletion; its separate secret belongs to the database and must stay with backups. WAL `synchronous=NORMAL` preserves the ledger across application-process crashes, but a host crash or power loss can lose a committed reservation and permit a duplicate. SES cannot guarantee exactly-once delivery, so this policy can omit an email when the first request actually failed but its outcome cannot be proven. See [sender performance and durability](docs/performance-profile.md).

## Maintenance, backup and restore

Maintenance purges raw tracking/SES history in bounded batches, retains aggregates, refreshes reporting rollups, removes expired unconfirmed subscriptions and schedules daily online backups. Cleanup measures the current confirmation period, so a fresh resubscription receives the full confirmation window. Migration `018_confirmation_lifecycle.sql` records that period separately from the original membership creation time. A confirmation whose membership has expired or been removed returns an error instead of reporting success without a subscription. Backups, housekeeping and brand SES suppression sync use durable recurring schedules independent of handler success. Exhausted retries do not stop future runs. Schedules survive restarts, avoid overlapping pending/running occurrences and skip missed intervals; repair a failure and subsequent scheduled runs continue. Tune raw retention using `POSTBOX_RETENTION_DAYS`; backups use `POSTBOX_BACKUP_DIR`.

Manual online backup:

```sh
POSTBOX_BACKUP_FILE=/safe/path/postbox-YYYY-MM-DD.sqlite postbox backup --db /var/lib/postbox/postbox.db
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

## Campaign workflows and CSV

Set `POSTBOX_NOTIFICATION_FROM` to a verified sender and `POSTBOX_NOTIFICATION_TO` to comma-separated admin addresses to receive campaign start/finish notifications. These durable email jobs share the account limiter and sending switch. Double opt-in confirmations and optional goodbye messages use the same transactional email queue. CSV job payload files are private basenames under the database directory’s `jobs` subdirectory; imports stream and exports create mode-0600 files.

All CSV downloads, including subscriber export jobs and campaign recipient exports, use spreadsheet-safe cells. Values whose first non-whitespace, non-control, non-format character is `=`, `+`, `-`, or `@` (including their fullwidth forms) receive a leading apostrophe. CSV quoting still preserves commas, quotes, line breaks, and Unicode. The database and JSON API retain the original values. These CSV downloads are not lossless machine backups: reimporting them retains the safety apostrophe as data, because an importer cannot distinguish an added escape from an original apostrophe. Use the JSON API or database backups when exact values must roundtrip; there is no raw CSV download mode.

The raw HTML editor is authoritative. Email templates remain full documents including their head, styles and MSO comments. Review WYSIWYG conversions as a source diff to check that the email markup is preserved.

## Capacity testing

Use `go test ./...` and race checks for correctness, `make bench` for MIME/rendering performance, and `cmd/fakeses` plus `cmd/loadtest` for reproducible fake delivery runs. The fake sender is explicit tooling and is never selected by the production service. The load tool reports achieved rate, request latency, process resource use and SQLite writer latency. For capacity planning, run a representative full campaign on the deployment host, including other expected database activity. A short smoke test does not establish sustained throughput. See [sender profiling and pacing](docs/performance-profile.md) for recorded full-run results and their limitations.

Full fake-server scenarios run through the HTTP SES v2 adapter, without live credentials:

```sh
go run ./cmd/loadtest --recipients 350000 --rate 175 --message-bytes 100000 --output load-175.json
go run ./cmd/loadtest --recipients 350000 --rate 1000 --message-bytes 100000 --output load-1000.json
go run ./cmd/fakeses --listen 127.0.0.1:9001 --rate 175 --throttle-every 50 --server-error-every 100
```

Dependency checks run with `make security`: pinned `govulncheck` examines reachable Go vulnerabilities and `bun audit` checks frontend dependencies, including development tools. Both require network access. Run them before release and after dependency updates. Keep the Go version in `go.mod` and the Dockerfile aligned, and rebuild deployed binaries after toolchain security updates.
