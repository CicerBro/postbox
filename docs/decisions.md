# Implementation decisions

## HTTP and persistence

The application uses Go's standard `net/http` method and path routing and handwritten SQL, as specified in the build plan. SQLite work queues, explicit transactions and indexed audience queries remain visible in the code. An ORM or a router framework is not required for the implemented API.

## Campaign delivery safety

The user's later requirement that a recipient must never receive the same campaign twice supersedes the original plan's ambiguous-network retry and manual unknown-resend options. When SES acceptance is uncertain, the recipient stays `unknown` and Postbox does not dispatch that delivery again. SDK retries are disabled. Automatic retries are limited to errors that establish nonacceptance.

Crash recovery follows the same rule. Durable recipient attempts and a campaign/email dispatch ledger guard concurrent dispatch and survive subscriber deletion. The ledger uses a keyed digest of the normalized email rather than retaining an erased address.

SES does not provide an idempotency key for this raw-email operation. Postbox can enforce at-most-once dispatch after possible acceptance, but cannot guarantee exactly-once delivery by the provider. This policy can leave an email undelivered after an uncertain response.

The user explicitly allows deliberate test sends. They are marked `[Test]` and are separate from campaign delivery. Duplicate addresses within a test request are normalized and collapsed before delivery.

## Authentication

Authentication includes password plus enrolled TOTP and passkeys. Passkeys require authenticator user verification, such as a device PIN or biometric check. WebAuthn ceremonies are bound to the configured application origin and use short-lived, single-use server-side challenges.

## Email source fidelity

Raw full-document HTML is authoritative. A visual editor may normalize HTML, so its proposed output must be reviewed before replacing the saved source. Preview and delivery share the Go renderer.
