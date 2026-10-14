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

The native binary does not load `.env` automatically. Export its variables or let your supervisor load an environment file. Explicit CLI flags override environment values, which override built-in defaults. Restart after changing startup configuration. Brand settings and sending controls saved through the UI take effect immediately.

## Configure HTTPS and proxy access

Set `POSTBOX_BASE_URL` to the exact browser origin, with no path, query or fragment. Preserve Host at the proxy and expose the public tracking, signup and `/events/sns` paths. Disable buffering for live campaign reports and allow long-lived connections. Restrict `/metrics` to your monitoring network.

Postbox derives passkey trust from the configured origin, rather than request headers. Passkeys require HTTPS, except for local HTTP on `localhost`; an IP address is not a supported relying-party domain. Cookie security is configured explicitly and does not depend on forwarding headers.

By default Postbox trusts no proxy headers. Set `POSTBOX_TRUSTED_PROXIES` to the specific IP addresses or CIDRs of proxies you control. A same-host example is `127.0.0.1,::1`; a container may instead see a bridge or gateway address. Avoid broad client networks and prevent clients from bypassing the proxy.

### nginx on the host, Postbox in Docker

nginx can log the correct visitor IP while Postbox still sees the proxy address. Cloudflare real-IP handling changes nginx's client address; nginx opens a separate connection to Postbox. With nginx on the host and Postbox behind Compose's published `127.0.0.1:8080` port, the container may see the Docker network gateway as the connecting peer. Trust that address in Postbox even though nginx itself is not containerised.

Forward nginx's resolved visitor address in the Postbox proxy configuration:

```nginx
proxy_set_header Host $host;
proxy_set_header X-Forwarded-For $remote_addr;
```

This assumes nginx's Cloudflare real-IP configuration already trusts the appropriate Cloudflare proxies. Postbox reads `X-Forwarded-For`; it does not use `CF-Connecting-IP` or `X-Real-IP` directly.

Find the container's network gateway:

```sh
docker inspect "$(docker compose ps -q postbox)" --format '{{range .NetworkSettings.Networks}}gateway={{.Gateway}}{{println}}{{end}}'
```

Add that exact address to `POSTBOX_TRUSTED_PROXIES` in the server's `.env`, alongside any needed loopback addresses. For a gateway of `172.20.0.1`:

```dotenv
POSTBOX_TRUSTED_PROXIES=127.0.0.1,::1,172.20.0.1
```

Use your gateway value; Docker network addresses vary between deployments and can change when networks are recreated. The loopback values in `.env.example` alone may not cover the host-to-container connection.

Recreate the container after changing `.env`:

```sh
docker compose up -d --force-recreate postbox
```

`docker compose restart` retains the container's old environment. Editing `.env.example` also does not change the deployed `.env`. Check just the effective tracking settings without displaying other environment values:

```sh
docker inspect "$(docker compose ps -q postbox)" --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -E '^POSTBOX_(GEOIP|TRUSTED_PROXIES)='
```

Each trusted proxy must overwrite incoming `X-Forwarded-For` at the first ingress or append the actual peer when retaining a chain. Postbox reads the chain from right to left and stops at the nearest untrusted IP. `Forwarded` and `X-Real-IP` are ignored. Invalid entries or an oversized chain fall back to the socket peer.

If Cloudflare proxies the instance, configure a [Browser Integrity Check skip rule](configuration.md#browser-integrity-check) for the deployment's hostname and the `/u/` and `/c/` path prefixes. This allows unsubscribe requests and tracked click redirects from email clients and link scanners to reach Postbox.

## Configure country tracking

Set `POSTBOX_GEOIP` (or `--geoip`) to a readable GeoLite2 Country or GeoIP2 Country/City MMDB file and restart Postbox. Country lookup uses the client IP resolved through `POSTBOX_TRUSTED_PROXIES`, so configure the proxy chain as described above. Private or unknown IPs have no country and do not create country report rows.

Compose uses `/var/lib/postbox/GeoLite2-Country.mmdb` inside the `postbox-data` volume. Copy your MMDB there or add a read-only bind mount for your local file at that container path. Ensure UID `65532` can read it. A file on the host is only available to the container when copied into its volume or mounted.

Website links in sent HTML use `/c/` redirects and count as clicks. The “view online” link uses `/w/` and does not itself count as a click. Opening that browser version can load the tracking pixel and record an open. Country rows are recorded for new opens and clicks whose IP resolves in the database; enabling GeoIP does not backfill earlier activity.

### Clicks work but the country report is empty

Click counters are saved even when country lookup returns no result. Check that the running container has the expected `POSTBOX_GEOIP` and `POSTBOX_TRUSTED_PROXIES` values, the MMDB is readable at the container path, and nginx forwards the resolved visitor IP. In the host-nginx setup above, trusting only loopback can make GeoIP look up Docker's private gateway instead of the visitor's public address.

After applying the settings, click a website link in a sent email and refresh the campaign report. Use a new event: previous activity cannot be backfilled because its source IP is not retained.

## Restart and shutdown timing

`POSTBOX_SHUTDOWN_TIMEOUT` defaults to `60s`; Compose allows `75s` before forcibly stopping the container. These are maximum waiting periods, not intentional delays. A recreate that consistently takes around 60 seconds can mean the old process spends its full shutdown budget waiting for work to finish before the replacement starts.

Campaign report pages keep a live HTTP connection open for statistics updates. Older builds did not cancel that request when receiving `SIGTERM`, so an open report could hold shutdown until the 60-second timeout. If recreating is fast with report tabs closed but slow while they are open, this connection is holding shutdown open.

Postbox now cancels request contexts on termination, closes the live report streams, and flushes queued tracking events before exiting. Until you deploy a build with this fix, close Postbox report tabs before recreating the container. Keep the supervisor's stop grace period longer than Postbox's shutdown timeout so pending work has time to finish.

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

Logs go to stderr and appear in `docker compose logs -f postbox` or `journalctl -u postbox -f`. HTTP client failures (4xx) use `WARN`; server failures (5xx), response write failures and handler panics use `ERROR`. Request logs include the method, route pattern, response status and elapsed milliseconds. Server errors include the underlying cause when available. Route patterns omit signed path values, and request logs omit query parameters, headers and bodies. Successful requests use `DEBUG` and are hidden at the default log level.

Job handler failures include the job ID, queue, attempt and resulting status: retryable failures use `WARN`, exhausted or unsafe-to-retry failures use `ERROR`. Lease renewal and completion failures also produce error logs. Campaign sending failures include the campaign ID and cause before the engine attempts to pause sending.

Monitor failed jobs and your AWS dead-letter queues as well as HTTP health. Daily jobs create online backups; hourly housekeeping refreshes rollups and purges raw tracking/SES history and expired failed jobs in bounded batches. Raw history defaults to 90 days; aggregate reports remain. Unconfirmed memberships have a separate 30-day confirmation period. Failed jobs default to 30 days from their terminal failure. Administrators can change the application-wide period under **Administration > Application policies** (`/admin/policies`); saved changes apply on the next hourly run. See the [configuration reference](configuration.md) for the API and upgrade behavior.

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
