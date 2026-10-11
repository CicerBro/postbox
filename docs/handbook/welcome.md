# Welcome to Postbox

Postbox is a self-hosted newsletter service using Amazon SES v2 and SQLite. A single Go binary embeds the React application and database migrations. Run it directly or with Docker. SQLite and durable jobs remove the need for a separate database or queue server; JavaScript is needed only when building the frontend. AWS SES delivers email, with SNS HTTPS notifications for delivery events.

![Postbox campaigns with sample test campaigns](../images/postbox-campaigns.png)

Campaign overview using synthetic test campaign data, with sending paused.

- Multiple brands with separate members, SES credentials, subscriber consent and event processing.
- Lists, segments, custom fields, reusable templates and a source-preserving HTML email editor.
- Scheduled campaigns, live reports, click/open tracking and signed unsubscribe links.
- Double opt-in, brand marketing opt-outs, delivery suppressions and configurable public signup.
- Durable jobs for imports, exports, transactional mail, maintenance and online backups.
- Password/TOTP and passkey authentication, brand-scoped API credentials and Sendy migration.

## Find your way

| You want to… | Start here |
| --- | --- |
| Set up an instance and connect Amazon SES | [Getting started](getting-started.md) |
| Check AWS permissions and event resources | [SES & SNS Setup](aws.md) |
| Build an audience and send a campaign | [Sending newsletters](sending-newsletters.md) |
| Deploy, secure or back up an instance | [Running Postbox](running-postbox.md) |
| Look up commands, flags and environment variables | [Configuration reference](configuration.md) |
| Bring your existing Sendy data across | [Moving from Sendy](moving-from-sendy.md) |
| Work on the code or measure performance | [Development](development.md) |
| Integrate another application | [API reference](/docs/api) |

Read the handbook in order for a first installation, or use search to jump to a task. SES/SNS setup and server configuration have dedicated reference pages.
