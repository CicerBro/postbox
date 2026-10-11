# Sending newsletters

A campaign combines a brand, an eligible audience and a finished email. Work through those three pieces before enabling or scheduling delivery.

## Build your audience

Use lists to organise subscriptions and segments to select contacts by criteria. Custom fields hold the information you need for targeting and personalisation. Import contacts through the subscriber workflow, then review the job result and membership states before sending.

CSV imports do not send confirmation or welcome emails, call external APIs, or enqueue outbound webhooks. Imported members of double opt-in lists remain unconfirmed until confirmation; importing them does not email a confirmation request. Existing campaigns and background jobs continue independently if the service is running.

Subscription and delivery eligibility are separate:

| State | What it means for sending |
| --- | --- |
| Subscribed | Eligible for the relevant list, subject to other consent and suppression checks |
| Unconfirmed | Waiting for double opt-in confirmation; excluded from marketing |
| Unsubscribed from a list | Excluded from that list; another active list can still be eligible |
| Brand marketing opt-out | Excluded from all campaigns in that brand |
| Delivery suppression | Excluded even when subscription consent is present |

Ordinary profile edits, CSV imports and list additions cannot undo a brand marketing opt-out. Resubscription requires fresh consent and a list choice. A double opt-in list also requires confirmation. Removing a delivery suppression alone does not establish consent.

## Accept new signups

Open **Settings > Public subscriptions** to control hosted and embedded signup forms. Both the application switch and the selected brand switch must be on. An administrator controls the application switch; brand writers control their brand's switch. Changes take effect immediately.

Disabling signup returns HTTP 403 for the hosted form and submissions and prevents new embed forms from being generated. Existing confirmation and unsubscribe links continue to work. For bot protection, configure both Turnstile keys in the [server configuration](configuration.md).

Anonymous signup does not overwrite an existing subscriber's name, custom fields or delivery state. Use the authenticated subscriber editor for profile changes. Fresh resubscription starts a new confirmation period; unconfirmed memberships expire after 30 days.

## Write and test the email

Start with a reusable template or edit the campaign's HTML directly. The raw HTML is authoritative: complete documents, head styles, table layouts and MSO conditional comments are preserved. If you use the visual editor, review its proposed source diff before accepting a conversion.

Check the subject, plain-text content, personalisation and links. Send a test to addresses you control and inspect it in the email clients your audience uses. Test messages have a `[Test]` subject prefix. Their browser-view and unsubscribe links open previews that expire after seven days. Unsubscribe previews never change subscriptions, and test messages do not record opens or clicks. Addresses are normalised and deduplicated within each test request; another requested test is a new send.

## Send or schedule

Before starting a campaign, review its checklist:

- The selected brand has working AWS keys, a verified sender and the correct SES region.
- The intended lists or segment contain the right contacts and respect consent and suppressions.
- The email renders correctly, links resolve, and unsubscribe links are present.
- The account has sufficient quota and its event destination is configured.
- Sending is enabled in Settings.

Start the campaign or choose its scheduled delivery time. Postbox prepares an audience snapshot, then checks eligibility again before dispatch. Its sender adapts concurrency to the SES quota and measured request latency. Campaigns sharing the same AWS account and region share the rate limit.

Use the live report to follow progress, acceptance, tracking and delivery outcomes. Report streams close when a session expires, a credential is revoked or access to the campaign is removed.

## Understand delivery outcomes

An accepted send means SES accepted the request. Later delivery events provide further outcomes. Hard bounces and complaints suppress the address within the originating brand and skip that brand's pending recipients. Transient bounces reach suppression after the configured threshold, which defaults to three. Explicit global email or domain suppressions apply across brands.

If a request times out, a connection drops after dispatch or the process is interrupted, its outcome can be **unknown**. Postbox blocks another potentially accepted send to that recipient for the campaign. A trusted SES event can later reconcile the original attempt without inflating counts.

> An unknown recipient cannot be manually retried. This avoids sending the same campaign twice when SES may already have accepted it. Only a proven nonacceptance, such as throttling or failure to establish a connection, allows bounded retries.

The dispatch ledger survives subscriber deletion and recreation. It protects against duplicate dispatch after a process crash, but SQLite's `synchronous=NORMAL` setting can lose recent commits after a host crash or power failure. SES and Postbox cannot guarantee exactly-once delivery. See [Development](development.md#how-the-service-fits-together) for the durability boundary.

## Export data and receive notifications

Subscriber and campaign recipient CSV exports protect spreadsheet users by adding a leading apostrophe to values that could be interpreted as formulas. The rule covers `=`, `+`, `-`, `@` and their fullwidth forms after leading whitespace, control or format characters. Quoting preserves commas, line breaks and Unicode.

Reimporting such a CSV retains the apostrophe as data. Use the JSON API or a database backup when you need exact values to round-trip. The database and JSON API keep the original values.

For campaign start and finish emails, set `POSTBOX_NOTIFICATION_FROM` to a verified sender and `POSTBOX_NOTIFICATION_TO` to comma-separated recipient addresses. These durable jobs use the campaign's brand connection, shared rate limiter and sending switch. Double opt-in confirmations and optional goodbye emails use the same transactional queue. Monitor failed jobs if expected emails do not arrive.
