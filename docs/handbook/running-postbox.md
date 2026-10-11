# Running Postbox

Run one service per database, put HTTPS in front of it, and keep a restorable copy of the database and secret. This chapter covers deployment and the routine work that keeps an instance healthy.

## Deploy the service

Choose Docker Compose or a native binary managed by systemd. Both use the same application and database. Production needs a public HTTPS origin, secure cookies and a TLS reverse proxy.

### Docker Compose

From the repository, copy the runtime example and set `POSTBOX_BASE_URL` to your public origin, such as `https://mail.example.com`:

```sh
cp .env.example .env
chmod 600 .env
docker compose build --pull
docker compose run --rm postbox init
```

Supply the administrator email, password and base32 TOTP secret through a protected shell environment. Keep those bootstrap secrets out of `.env`, then forward them to the one-off command:

```sh
docker compose run --rm \
  -e POSTBOX_ADMIN_EMAIL -e POSTBOX_ADMIN_PASSWORD -e POSTBOX_ADMIN_TOTP \
  postbox admin
unset POSTBOX_ADMIN_PASSWORD POSTBOX_ADMIN_TOTP
docker compose up -d
docker compose logs -f postbox
curl --fail http://127.0.0.1:8080/healthz
```

The [Compose file](../../compose.yml) publishes HTTP on host loopback port 8080. Change `POSTBOX_PORT` if needed. A proxy in another container should share the Compose network and connect to `postbox:8080`.

The image runs as UID/GID `65532:65532`, with a read-only root filesystem and no shell. The `postbox-data` volume holds the database, WAL files, `tracking.secret`, job payloads and backups. Preserve that volume across deployments. A bind mount must be writable by this UID/GID; use a private directory with mode `0700`.

Rebuild with `docker compose build --pull` and deploy with `docker compose up -d`. `docker compose down` preserves the named volume; adding `-v` deletes it. Run external health probes because the image has no probe utility.

### Native binary with systemd

Install the binary at `/usr/local/bin/postbox` and create a `postbox` system account. Use [the environment example](../../deploy/postbox.env.example) at `/etc/postbox/postbox.env` with mode `0600`, and [the service unit](../../deploy/postbox.service) at `/etc/systemd/system/postbox.service`.

Set the public origin, initialise the database and create the administrator as the service user using that same environment. Then start the service:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now postbox
sudo journalctl -u postbox -f
```

The native binary does not load `.env` automatically. Export its variables or let your supervisor load an environment file. Explicit CLI flags override environment values, which override built-in defaults. Restart after changing startup configuration. Brand settings and sending controls saved through Settings take effect immediately.

## Configure HTTPS and proxy access

Set `POSTBOX_BASE_URL` to the exact browser origin, with no path, query or fragment. Preserve Host at the proxy and expose the public tracking, signup and `/events/sns` paths. Disable buffering for live campaign reports and allow long-lived connections. Restrict `/metrics` to your monitoring network.

Postbox derives passkey trust from the configured origin, rather than request headers. Passkeys require HTTPS, except for local HTTP on `localhost`; an IP address is not a supported relying-party domain. Cookie security is configured explicitly and does not depend on forwarding headers.

By default Postbox trusts no proxy headers. Set `POSTBOX_TRUSTED_PROXIES` to the specific IP addresses or CIDRs of proxies you control. A same-host example is `127.0.0.1,::1`; a container may instead see a bridge or gateway address. Avoid broad client networks and prevent clients from bypassing the proxy.

Each trusted proxy must overwrite incoming `X-Forwarded-For` at the first ingress or append the actual peer when retaining a chain. Postbox reads the chain from right to left and stops at the nearest untrusted IP. `Forwarded` and `X-Real-IP` are ignored. Invalid entries or an oversized chain fall back to the socket peer.

## Manage access

Password login requires an authenticator code when TOTP is enabled. Enrolment starts with password confirmation and finishes after a valid code; removal requires the password and current code. Changing a password or TOTP factor revokes existing sessions.

Passkeys use device verification such as a PIN or biometrics. Adding or removing one requires password confirmation and the existing TOTP code when enabled. Removing a passkey revokes the user's sessions and pending passkey ceremonies.

User sessions carry CSRF protection. A token remains stable across tabs, reloads and server restarts for that session. Logout, expiry and revocation invalidate it. Brand API credentials are scoped to their brand and stored as hashes.

The handbook, Scalar reference, assets and OpenAPI downloads require an active user session. Brand API keys do not grant access to `/docs`. Inside Scalar you can try requests with your session or enter a brand API key to check its permissions.

Public authentication and signup operations have per-process rate limits. Account factor operations use the acting user ID; public operations use the canonical client IP. Details are in the [configuration reference](configuration.md).

## Back up and restore

A usable backup needs both the database and the matching tracking/credential secret. The native default secret is the database path plus `.secret`; Compose uses `/var/lib/postbox/tracking.secret`. Keep it private with mode `0600`. The dispatch ledger's separate secret is stored inside the database.

Create a manual online backup with a new output filename:

```sh
POSTBOX_BACKUP_FILE=/safe/path/postbox-YYYY-MM-DD.sqlite \
  postbox backup --db /var/lib/postbox/postbox.db
```

For Compose:

```sh
docker compose run --rm \
  -e POSTBOX_BACKUP_FILE=/var/lib/postbox/backups/manual-YYYY-MM-DD.sqlite \
  postbox backup
```

Backups use SQLite `VACUUM INTO` and remain consistent while the service is running. Copy the resulting backup and secret off-host, and periodically test a restore.

To restore, stop Postbox first, replace the database with the consistent backup and restore its matching secret. Preserve the service user's ownership and private permissions before starting again. Do not copy only a live `.db` file while ignoring its WAL. Optional Litestream replication is external infrastructure and must follow its SQLite/WAL restore procedure.

## Monitor and maintain

The service writes structured JSON logs. `/healthz` checks database availability. `/metrics` exposes sending rates, backlog, in-flight requests, latency, errors, writer batches, job outcomes and dropped tracking events.

Monitor failed jobs and your AWS dead-letter queues as well as HTTP health. Daily jobs create online backups, refresh rollups and purge raw tracking/SES history in bounded batches. Raw history defaults to 90 days; aggregate reports remain. Unconfirmed memberships have a separate 30-day confirmation period.

Recurring schedules survive restarts, prevent overlapping occurrences and skip missed intervals. A failed run does not stop future occurrences. Repair the cause and monitor the next run. Set `POSTBOX_BACKUP_DIR` to the backup directory, or explicitly empty it to disable recurring backups.

On SIGTERM, Postbox stops claiming recipients, drains in-flight sends and tracking writes, stops jobs, and optimises SQLite. The default shutdown timeout is 60 seconds; allow at least 75 seconds in the supervisor. Interrupted dispatches become unknown and can later be reconciled by SES events.

## Diagnose common problems

| Symptom | Check |
| --- | --- |
| Service will not start | Public origin, HTTPS/cookie settings, filesystem permissions, secret permissions and configured GeoIP file |
| AWS connection fails | Saved brand IAM keys, correct region, runtime IAM permissions and endpoint overrides |
| Domain setup fails | Setup IAM permissions for the specific identity, then DNS records and AWS verification status |
| Emails do not send | Sending switch, audience consent/suppressions, SES sandbox/quota and failed jobs |
| Confirmation email is missing | Sending switch, brand connection and transactional job errors |
| Passkey login fails | Browser origin exactly matches `POSTBOX_BASE_URL`; HTTPS or local `localhost` |
| All clients share a rate limit | Actual proxy peer matches the trusted list and ingress supplies a valid `X-Forwarded-For` chain |
| Campaign has unknown recipients | Wait for trusted SES events; do not reset the ledger or force a retry |
