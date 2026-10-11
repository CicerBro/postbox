# Postbox

Postbox is a self-hosted newsletter service using Amazon SES v2 and SQLite. A single Go binary embeds the React application and database migrations. Run it directly or with Docker. SQLite and durable jobs remove the need for a separate database or queue server; JavaScript is needed only when building the frontend. AWS SES delivers email, with optional SNS/SQS for delivery events.

![Postbox campaigns with sample test campaigns](docs/images/postbox-campaigns.png)

Campaign overview using synthetic test campaign data, with sending paused.

- Multiple brands with separate members, SES credentials, subscriber consent and event processing.
- Lists, segments, custom fields, reusable templates and a source-preserving HTML email editor.
- Scheduled campaigns, live reports, click/open tracking and signed unsubscribe links.
- Double opt-in, brand marketing opt-outs, delivery suppressions and configurable public signup.
- Durable jobs for imports, exports, transactional mail, maintenance and online backups.
- Password/TOTP and passkey authentication, brand-scoped API credentials and Sendy migration.

## Quick start

Build with Go matching [go.mod](go.mod) and Bun 1.4.3:

```sh
make build
export POSTBOX_DB=./data/postbox.db
export POSTBOX_BASE_URL=http://localhost:8080
./bin/postbox init
```

Supply `POSTBOX_ADMIN_EMAIL`, `POSTBOX_ADMIN_PASSWORD` and `POSTBOX_ADMIN_TOTP` through a protected environment. The TOTP value is the base32 secret enrolled in your authenticator. Create the administrator, clear the bootstrap secrets, then start the local application:

```sh
./bin/postbox admin
unset POSTBOX_ADMIN_PASSWORD POSTBOX_ADMIN_TOTP
./bin/postbox serve --development --secure-cookies=false
```

Open <http://localhost:8080>. Sending starts disabled. Configure a brand's AWS keys and SES region under **Settings > Amazon SES**, review sending identities and consent, then enable sending in Settings.

For production, use an HTTPS origin, secure cookies and a TLS reverse proxy. The [manual](MANUAL.md) covers Docker Compose, systemd, AWS/IAM, event ingestion, security, backups and migration. Back up the database and its tracking/credential encryption secret together.

## Architecture

Postbox is a modular Go application with one deployable process:

| Module | Responsibility |
| --- | --- |
| `web` / `internal/httpapi` | React workflows, generated API contracts, authentication and HTTP transport |
| `internal/subscribers` | Subscriber profiles, subscription and confirmation lifecycle, batched imports |
| `internal/consent` | Transaction-scoped consent transitions shared by subscribers, tracking and SES events |
| `internal/send` | Audience snapshots, dispatch reservations, SES pacing and send outcomes |
| `internal/events` / `internal/tracking` | Verified SES events, acceptance reconciliation and signed tracking links |
| `internal/jobs` | Durable job leases, independent recurring schedules, imports and maintenance |
| `internal/store` | Migrations, pooled reads and serialized write transactions |

Subscriber services, tracking endpoints and SES ingestion reuse transaction-scoped consent transitions. HTTP authorization also supplies the identity checks used by live report streams, and job recurrence is managed independently of handler success. Campaign dispatch reserves a durable attempt before contacting SES. An uncertain outcome blocks another potentially accepted send; a later SES event can reconcile the original attempt.

SQLite uses WAL, one writer and a reader pool. This keeps deployment simple and makes short write transactions important. Imports prepare and reuse list metadata per batch, event matching uses indexed keys, and maintenance runs in bounded batches. WAL `synchronous=NORMAL` protects against process crashes but recent commits can be lost after a host crash or power failure. See [performance and durability evidence](docs/performance-profile.md) for measurements and limits.

## Development and tests

Use Bun for frontend dependencies. Build embedded frontend assets before compiling or testing a fresh checkout:

```sh
make build
(cd web && bunx playwright install chromium)
make test
make race
go vet ./...
go run ./cmd/contractgen -check
make security
```

`make test` runs Go tests, frontend unit tests and Playwright browser tests. `make test-browser` runs the browser suite alone. Browser tests use a local Vite server and mocked API responses, while Go integration tests exercise SQLite and fake SES delivery.

After editing Go API response structs, regenerate frontend contracts with `go run ./cmd/contractgen`. For UI development run `cd web && bun run dev`; `bun run build` checks TypeScript and builds the embedded assets. CI checks contracts, builds, dependencies, Go race tests, vet, frontend unit/browser tests and CGO-free Linux builds.

`make bench` benchmarks MIME/rendering and sending. `make release` creates Linux amd64 and arm64 binaries. See the [manual](MANUAL.md#capacity-testing) for representative full-campaign load tests and [Sendy migration reference](docs/migration.md) for importer mappings and limits.
