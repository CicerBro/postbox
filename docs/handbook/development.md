# Development

Postbox ships as one Go process with an embedded React application, handbook and Scalar reference. Build the frontend before compiling or testing a fresh checkout.

## Build and work locally

Use the Go version declared in [go.mod](../../go.mod) and Bun 1.4.3:

```sh
make build
(cd web && bunx playwright install chromium)
```

`make build` installs frontend dependencies, checks TypeScript, builds the application and docs, then compiles `bin/postbox`. A deployed binary needs neither Bun nor Node. Follow [Getting started](getting-started.md#start-an-instance) to initialise a local database and administrator.

For frontend development, run `cd web && bun run dev`. Use `bun run dev:docs` for the handbook. Both need the local backend for authentication and API access. For passkey testing, use the embedded Go application at its configured localhost origin so browser and relying-party origins match.

## Validate a change

```sh
make test
make race
go vet ./...
go run ./cmd/contractgen -check
make security
```

`make test` runs Go, frontend unit and Playwright browser tests. `make test-browser` runs the browser suite alone. Browser tests use local Vite servers and mocked responses; Go integration tests exercise SQLite and fake SES delivery. The race suite checks concurrent Go paths. Dependency audits require network access.

After changing API response structs, regenerate frontend contracts with `go run ./cmd/contractgen`, then check them again. `bun run build` checks TypeScript and rebuilds embedded assets. CI also verifies CGO-free Linux builds. `make release` creates Linux amd64 and arm64 binaries.

## How the service fits together

| Area | Responsibility |
| --- | --- |
| `web`, `internal/httpapi` | User workflows, API contracts, authentication and HTTP transport |
| `internal/subscribers`, `internal/consent` | Profiles, membership lifecycle and shared consent transitions |
| `internal/send` | Audience snapshots, durable dispatch reservations, SES pacing and outcomes |
| `internal/events`, `internal/tracking` | Verified SES events, reconciliation and signed links |
| `internal/jobs` | Durable leases, imports, exports and recurring maintenance |
| `internal/store` | Migrations, pooled reads and serialised write transactions |

SQLite uses WAL, a reader pool and one writer. Keep write transactions short. Imports reuse metadata per batch, event matching uses indexed keys and maintenance works in bounded batches. Recurring job schedules advance independently of handler success.

A campaign reserves a durable attempt before contacting SES. Uncertain acceptance blocks a second dispatch; a trusted event can reconcile the original attempt. The address-HMAC ledger survives subscriber deletion and is backed up with the database.

WAL `synchronous=NORMAL` preserves committed reservations across a process crash. A host crash or power loss can lose recent commits, including reservations, and permit a duplicate. Preserve that distinction in reliability claims and tests. The policy also permits a missed email when nonacceptance cannot be proven.

## Database schema changes

The complete fresh-install schema lives in `internal/store/migrations/001_initial.sql`. The pre-production migration history was consolidated into this baseline. Older development databases need a fresh database as described in [Upgrade notes](configuration.md#upgrade-notes).

Add future schema changes as new numbered SQL files in `internal/store/migrations/`, starting with `002_`. The store embeds these files, applies them in filename order within a write transaction and records their SHA-256 checksums. Leave applied migration files unchanged so existing databases can reopen safely. Validate fresh installation and reopening with `go test ./internal/store`, then run the Go suite for affected workflows.

## Edit the handbook

The eight chapters in `docs/handbook/` are the canonical prose for `/docs`. Their order is declared in [the guide builder](../../web/docs/content.ts). The builder renders Markdown, resolves local chapter links and lists SES & SNS Setup and Configuration reference as standalone pages in the sidebar. Sendy adapter details live in Moving from Sendy. The README links to these chapters.

Edit the relevant chapter, run the docs tests and rebuild with `make build`. The compiled HTML, search text, Scalar code, styles and image are embedded through `internal/web/dist`. Serving docs requires a user session.

The API reference has a separate source: Go handlers and schema helpers in `internal/httpapi`, including `openapi.go`. Changing handbook text does not change the specification.

## Measure capacity

Use `make bench` for rendering and sender microbenchmarks. For a full campaign, run the load tool on the intended host with representative message sizes and other expected database activity:

```sh
go run ./cmd/loadtest --recipients 350000 --rate 175 \
  --message-bytes 100000 --output load-175.json
go run ./cmd/loadtest --recipients 350000 --rate 1000 \
  --message-bytes 100000 --output load-1000.json
```

These runs use an explicit fake HTTP SES fixture with real SDK serialisation. For local application QA, start `cmd/fakeses`, set `AWS_ENDPOINT_URL`, and save fake credentials and a region for the test brand. The emulator must also support STS `GetCallerIdentity`. Do not point real brand keys at a test endpoint.

A short smoke test does not establish sustained throughput. Compare complete-campaign rate, request latency, writer latency and peak resources, and account for fixture overhead and host contention. Record the results for your deployment alongside its configuration and workload.
