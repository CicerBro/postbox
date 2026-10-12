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

## Settings and access

Settings are organized by the scope of a change. Personal preferences belong to your account, brand settings apply to the named brand, and Administration applies to the whole installation. Each section has a bookmarkable URL. Switching brands leaves account and administration pages in place.

| Area | Pages | Access |
| --- | --- | --- |
| My account | Security (`/account/security`), Preferences (`/account/preferences`) | Your own account |
| Brand settings | General (`/brands/:brandId/settings/general`), Sending & domains (`/brands/:brandId/settings/sending`), Subscriptions (`/brands/:brandId/settings/subscriptions`), Integrations (`/brands/:brandId/settings/integrations`), Danger zone (`/brands/:brandId/settings/danger`) | Brand access, with editing determined by your permissions |
| Administration | Users & access (`/admin/users`), Application policies (`/admin/policies`), Operations (`/admin/operations`), Global suppressions (`/admin/suppressions`), Deployment (`/admin/deployment`) | Administrators only |

Replace `:brandId` with the selected brand's ID. AWS credentials, domain configuration, API key management, sender identity changes, and brand deletion remain administrator actions within that brand's context. Brand writers can edit subscription defaults, signup availability, webhooks, and brand suppressions; brand readers see those controls without editing access.

Preferences includes appearance and timezone. Appearance is saved in the current browser and stays synchronized with the header shortcut. Timezone is saved to your account and follows you between browsers.

Operations contains the sending switch for all brands and failed-job monitoring. Application policies contains the global public-signup switch and failed-job retention. Deployment shows safe status for startup configuration; environment variables and startup flags are changed on the host, followed by a restart.

Global suppressions apply to all brands. Brand suppressions live with Subscribers; inherited global blocks are visible there but can only be changed in Administration. List-specific subscription configuration stays with its list. Subscription emails and hosted pages inherit from Postbox defaults through brand defaults to list overrides.
