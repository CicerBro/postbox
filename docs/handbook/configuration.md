# Configuration reference

Use `postbox <command> [flags]`. Put flags after the command and use `--secure-cookies=false` for a false boolean value. `postbox help` lists commands; `postbox <command> --help` prints flags without opening the database.

## Commands

| Command | Purpose | Required environment |
| --- | --- | --- |
| `serve` | Apply migrations and start HTTP and background work | Valid `POSTBOX_BASE_URL` |
| `init` | Apply migrations and create or validate the server secret | None |
| `admin` | Create an administrator with password and TOTP | `POSTBOX_ADMIN_EMAIL`, `POSTBOX_ADMIN_PASSWORD`, `POSTBOX_ADMIN_TOTP` |
| `backup` | Create a consistent online database backup | `POSTBOX_BACKUP_FILE` |
| `migrate-sendy` | Import a Sendy source and print a JSON report | `POSTBOX_SENDY_SOURCE_ID` and one source variable |

All commands open the configured database, apply pending migrations and initialise the sending safety switch once. Only `serve` requires a public origin and enforces production HTTPS and secure cookies.

## Startup flags

Explicit flags override environment values, which override these native defaults. The binary does not read `.env` automatically. Restart after changing startup configuration.

| Flag | Environment variable | Default | Meaning |
| --- | --- | --- | --- |
| `--db` | `POSTBOX_DB` | `./data/postbox.db` | SQLite file to open or create. Relative paths resolve from the working directory. The default secret path and CSV job directory are derived from this path. |
| `--listen` | `POSTBOX_LISTEN` | `127.0.0.1:8080` | HTTP bind address and port. Use `0.0.0.0:8080` when the listener must be reachable outside its network namespace. This does not change the public URL. |
| `--base-url` | `POSTBOX_BASE_URL` | Empty | Public origin used for email links, tracking, hosted forms and passkey validation. Required for `serve`; use an absolute origin without credentials, a non-root path, query or fragment. HTTPS is required outside development mode. |
| `--region` | `POSTBOX_AWS_REGION` | `AWS_REGION`, otherwise empty | Default region offered for a brand without saved AWS settings. A brand's saved region controls its AWS requests. This is a region, such as `eu-west-1`, not an availability zone. An explicitly empty `POSTBOX_AWS_REGION` suppresses the `AWS_REGION` fallback. |
| `--secret-file` | `POSTBOX_SECRET_FILE` | Configured database path plus `.secret` | File holding the generated 32-byte base64 tracking/credential encryption secret. An empty value uses the derived path. Existing files must be regular files with no group/other access; generated files use mode 0600. Back it up with the database. |
| `--geoip` | `POSTBOX_GEOIP` | Empty | Optional local MaxMind database path for tracking geolocation. Empty disables GeoIP lookup. An unreadable or invalid configured database prevents `serve` from starting. |
| `--backup-dir` | `POSTBOX_BACKUP_DIR` | `./data/backups` | Directory for recurring online backups. An empty string disables recurring backups. It does not set the output path for the `backup` command. |
| `--trusted-proxies` | `POSTBOX_TRUSTED_PROXIES` | Empty, trust none | Comma-separated proxy IP addresses or CIDRs allowed to supply `X-Forwarded-For`. Whitespace around entries is accepted; invalid addresses and empty entries are rejected. See [proxy configuration](running-postbox.md#configure-https-and-proxy-access). |
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

## Environment-only options

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

Sending, public signup switches, AWS keys and event resources are persisted application or brand settings. Set them through the UI or API; they have no main-binary environment flags.

## Value formats

Durations use Go syntax: `30s`, `5m`, `24h` or `1h30m`; `1d` is invalid. Worker counts, retention days, bounce thresholds and session/shutdown durations must be positive.

An explicitly empty string overrides a string default. Empty boolean, integer and duration variables use defaults. Environment booleans accept Go forms such as `true`, `false`, `1`, `0`, `t` and `f`. An invalid environment boolean silently uses the default; invalid integer/duration values become zero and fail positive-value validation unless a valid flag overrides them. Invalid CLI flag values are errors. `POSTBOX_SENDY_DRY_RUN` rejects invalid booleans.

## AWS and outbound connections

Brand requests use the brand's saved region and static credentials. `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, profiles, shared credential files and instance roles do not replace those credentials.

| Variable | Default | Meaning |
| --- | --- | --- |
| `AWS_REGION` | Empty | Fallback for the app's default brand region when neither `--region` nor `POSTBOX_AWS_REGION` provides one. Saved brand regions take precedence for requests. |
| `AWS_ENDPOINT_URL` | Normal AWS endpoints | SDK-wide endpoint override, such as `http://127.0.0.1:9001`. Applies to SES v2 and STS unless a service-specific override takes precedence. |
| `AWS_ENDPOINT_URL_SESV2` | Unset | Endpoint override for SES v2 requests only. |
| `AWS_ENDPOINT_URL_STS` | Unset | Endpoint override for STS account identity checks only. A local emulator must support `GetCallerIdentity` for connection checks and sending. |
| `AWS_IGNORE_CONFIGURED_ENDPOINT_URLS` | `false` | SDK boolean that ignores configured environment/shared-config endpoint URLs when enabled. |

The Go default transport also reads `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`. Other standard SDK and runtime variables belong to those libraries. Endpoint overrides still use the real SDK; they do not select an in-process fake sender.

## Containers and build tools

| Setting | Type and default | Meaning |
| --- | --- | --- |
| `POSTBOX_PORT` | Compose interpolation variable, `8080` | Host loopback port published as `127.0.0.1:PORT:8080`. It is not read by the binary and does not change the container listener or public origin. |
| `GO_VERSION` | Docker build argument, `1.27.2` | Go builder image version. Override with `docker build --build-arg GO_VERSION=...`; keep it aligned with `go.mod`. |
| `BUN_VERSION` | Docker build argument, `1.4.3` | Bun frontend builder image version. Override with `docker build --build-arg BUN_VERSION=...`. |
| `TARGETOS`, `TARGETARCH` | BuildKit platform arguments | Target operating system and architecture for the Go build, normally supplied by `docker buildx --platform`. These are build inputs, not runtime settings. |
| `GOVULNCHECK_VERSION` | Make variable, `v1.8.0` | Version used by `make security-go`. Override as `make security-go GOVULNCHECK_VERSION=...`. |
| `CI` | Browser-test environment variable, unset | Any nonempty value prevents Playwright from reusing an existing Vite server. The browser suite starts its own server at `127.0.0.1:5174` and a docs server at `127.0.0.1:5175`. |

The image defaults to `POSTBOX_DB=/var/lib/postbox/postbox.db`, `POSTBOX_SECRET_FILE=/var/lib/postbox/tracking.secret`, `POSTBOX_BACKUP_DIR=/var/lib/postbox/backups` and `POSTBOX_LISTEN=0.0.0.0:8080`. Its working directory is `/var/lib/postbox`. Compose supplies a 75-second stop grace period.

## Rate limits and proxy parsing

Limits use fixed one-minute windows in each process:

| Operation | Requests per window |
| --- | ---: |
| Password login and authenticated factor checks | 10 |
| Passkey operations and signup forms | 20 |
| Confirmation links | 30 |
| Authenticated API keys | 600 |

Factor operations use the acting account ID. Public operations use a canonical client IP without source ports; IPv4-mapped IPv6 shares the IPv4 bucket. Limits remain in memory, so multiple processes need an external shared limiter for aggregate limits. Expired buckets are removed during requests. At 10,000 active identities, new identities are denied without evicting existing throttles.

Only a trusted direct socket peer can supply `X-Forwarded-For`. Entries must be bare IPv4 or IPv6, without ports or zones. An invalid inspected suffix, more than 32 hops or more than 4096 bytes falls back to the direct peer. An invalid direct peer uses a shared `unknown` bucket. Earlier client-supplied entries cannot override the nearest untrusted hop.

## Cloudflare and HTML responses

HTML responses send `Cache-Control: no-transform` alongside their existing caching policy. The nonempty `Speculation-Rules: "data:application/speculationrules+json,%7B%7D"` header provides an empty rule set without a network request. Cloudflare replaces an empty header with its Speed Brain URL, which causes CORS errors from sandboxed previews. Keep Origin Cache Control enabled so Cloudflare respects the response directives.

The app, documentation and signup policies permit Cloudflare Web Analytics only from `https://static.cloudflareinsights.com`, with reporting to `https://cloudflareinsights.com`. Signup also permits Turnstile. Because `no-transform` stops automatic beacon injection, analytics requires an explicitly installed beacon snippet if wanted; allowing its origins does not enable analytics by itself.

Email previews and live browser-version emails stay script-free. Their links can open normal new tabs. Custom thank-you and goodbye HTML also keeps scripts disabled.

If a preview still reports blocked scripts, inspect the response for Cloudflare injection. Disable Rocket Loader and Web Analytics with a Cloudflare Configuration Rule scoped to the Postbox hostname and these HTML paths. Cloudflare JavaScript Detections should respect `no-transform`; verify that a Worker or response rule has not removed it. Do not enable scripts or same-origin access in the email sandbox to accommodate injected scripts. See [Cloudflare Configuration Rules settings](https://developers.cloudflare.com/rules/configuration-rules/settings/) and [JavaScript Detections](https://developers.cloudflare.com/cloudflare-challenges/challenge-types/javascript-detections/).

## Upgrade notes

Postbox has not been deployed to production. The development migration history has been consolidated into `001_initial.sql`, which creates the complete current schema for a fresh installation. Historical session resets, consent backfills and AWS credential conversions are no longer upgrade steps.

Databases created with the earlier development migrations cannot be opened with this baseline: startup reports `migration 001_initial.sql checksum changed`. Stop the old service and retain a backup with its matching secret if you need the development data. Initialise a new, empty database, recreate the administrator and brand settings, and rerun any Sendy import from its original source. Do not clear `schema_migrations` or edit its checksums to bypass the error; that does not recreate the schema.

Future schema changes will use new numbered migrations applied automatically when a command opens the database.
