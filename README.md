# Postbox

Postbox is a self-hosted newsletter service using Amazon SES v2 and SQLite. A single Go binary embeds the React application and database migrations. Run it directly or with Docker. SQLite and durable jobs remove the need for a separate database or queue server; JavaScript is needed only when building the frontend. AWS SES delivers email, with SNS HTTPS notifications for delivery events.

![Postbox campaigns with sample test campaigns](docs/images/postbox-campaigns.png)

Campaign overview using synthetic test campaign data, with sending paused.

- Multiple brands with separate members, SES credentials, subscriber consent and event processing.
- Lists, segments, custom fields, reusable templates and a source-preserving HTML email editor.
- Scheduled campaigns, live reports, click/open tracking and signed unsubscribe links.
- Double opt-in, brand marketing opt-outs, delivery suppressions and configurable public signup.
- Durable jobs for imports, exports, transactional mail, maintenance and online backups.
- Password/TOTP and passkey authentication, brand-scoped API credentials and Sendy migration.

Start with [Getting started](docs/handbook/getting-started.md) to install Postbox and connect Amazon SES. For production deployment and backups, see [Running Postbox](docs/handbook/running-postbox.md).

## Handbook

- [Welcome](docs/handbook/welcome.md): overview and where to start.
- [Getting started](docs/handbook/getting-started.md): installation, brand connection, domains and SES events.
- [SES & SNS Setup](docs/handbook/aws.md): IAM policies, SES/SNS setup and event processing.
- [Sending newsletters](docs/handbook/sending-newsletters.md): audiences, consent, content and delivery outcomes.
- [Running Postbox](docs/handbook/running-postbox.md): deployment, access, backups and configuration.
- [Configuration reference](docs/handbook/configuration.md): commands, flags, environment variables and rate limits.
- [Moving from Sendy](docs/handbook/moving-from-sendy.md): preview, import, review and retry.
- [Development](docs/handbook/development.md): architecture, build, tests and capacity testing.

After signing in, open **Documentation** in the sidebar or visit `/docs`. `make build` embeds the searchable handbook, Scalar API reference and their assets in the binary. The documentation, assets and OpenAPI downloads require an active user session. Edit `docs/handbook/*.md` to change the guides and detailed references.
