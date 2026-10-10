# Validation

Validated on 10 October 2026 with Go 1.27.1 and Bun 1.4.2 on an Apple M1 Pro
(macOS arm64).

## Automated checks

- `make test`: frontend preservation/theme tests and all Go packages.
- `make race`: all Go packages with the race detector.
- `go vet ./...`.
- Frozen Bun install, TypeScript checks and production frontend build.
- CGO-free, statically linked Linux amd64 and arm64 builds with the final
  embedded frontend.
- Query-plan tests inspect production SQL, including dispatch completion,
  recipient claims, audience snapshots, subscriber search and suppression scopes.
- Authentication tests use a signed ES256 virtual authenticator and cover
  WebAuthn origin, RP, user verification, challenges, replay, counters,
  ownership and session revocation, alongside TOTP and CSRF checks.
- CLI tests exercise first setup, persisted sending state, admin creation,
  backups, Sendy dry-run/idempotency, real SDK credential signing, missing
  credentials, health and SIGTERM shutdown.

Full campaign measurements and microbenchmark scope are recorded in
[performance-profile.md](performance-profile.md). Those measurements use the
real SES v2 HTTP adapter against fake SES, with a 100KB template and 350,000
recipients. They do not constitute a live AWS delivery test.

## Browser checks

The embedded Go application ran at `http://localhost:8080` with an isolated
test database and a loopback fake SES endpoint. Browser checks covered:

- TOTP sign-in, brand creation and sender identity settings.
- Subscriber creation, recorded consent and explicit list subscription.
- CSV mapping, dry-run, import and authenticated export, including quoted names.
- Segment creation and live count preview.
- Campaign audience selection, personalized Go-rendered preview and preflight.
- Two deliberate test requests to the same address and one campaign delivery:
  three fake acceptances, with one address deduplicated within each test request.
- Completed campaign report and connected SSE updates.
- Full-document template save, visual discard, visible source diff, and
  preserved head styles and Outlook conditional comments.
- Both the visible and hidden GrapesJS canvases are sandboxed before rendering.
- List-specific page settings persist and inherit defaults correctly.
- System, Light and Dark icon controls select the theme. Bootstrap tests cover
  first paint and saved preferences; System subscribes to OS appearance changes.
- Settings, account security and the API-key page load without browser errors.
- The admin UI directly adapts shadcn-admin's inset layout, compact sidebar,
  locally bundled Inter font and neutral OKLCH light/dark surfaces with amber accents. The palette
  uses Amber Minimal-inspired accents while retaining the compact reference layout.
- Reference comparison at 1762 by 1194 verifies the 256px sidebar, workspace
  button at (16,16) sized 224 by 48, first navigation row at (16,112) sized
  224 by 32, and main inset starting at x=256 with width 1498.
- The brand dropdown selects the current brand; page search filters routes
  and opens a result on Enter. Control K opens the search dialog; Escape closes it.
- Subscriber email-prefix filtering returns real backend results; list and campaign
  menus support keyboard dismissal, and campaign reports retain connected SSE.
- At 390px mobile width, account navigation closes the focus-trapped sheet.
  At 640px by 360px, the sidebar scrolls to expose its account/theme controls.
  Temporary viewport overrides were reset after these checks.

Physical passkey enrollment and live AWS infrastructure were not exercised.
The preview stays configured for loopback fake SES.

## Amber controls and subscriber terminology

The latest UI pass verified regular 400-weight brand inputs, left-aligned settings
navigation, Radix checkbox keyboard toggling, subscriber status filters including
returning to the empty "All statuses" value, and Enter opening a subscriber profile.
Native admin checkboxes and selects are replaced with shared Radix controls.
The sending action has a Pause icon and explicit "Pause sending" text when enabled.
White light-mode surfaces and neutral charcoal dark-mode surfaces were inspected;
amber remains confined to actions, highlights and charts. The existing Light
preference was restored.

Go package/API helper terminology now uses subscribers. Database tables and indexes
already use subscriber names; all nine applied migration files are unchanged.
The legacy `/contacts` route redirects to `/subscribers`, preserving query/hash.

![Amber overview and sending control](screenshots/amber-overview.jpg)

![Amber subscriber workspace](screenshots/amber-subscribers.jpg)

![Neutral charcoal dark mode](screenshots/amber-dark-overview.jpg)
