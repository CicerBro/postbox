# Getting started

Get Postbox running, connect a brand to Amazon SES, and prepare your first send. Sending stays disabled until you turn it on in Settings.

## Start an instance

For a production installation, follow the [Docker or systemd deployment steps](running-postbox.md#deploy-the-service). For a local instance, build the binary with the Go version in [go.mod](../../go.mod) and Bun 1.4.3:

```sh
make build
export POSTBOX_DB=./data/postbox.db
export POSTBOX_BASE_URL=http://localhost:8080
./bin/postbox init
```

Supply these variables through a protected environment before creating the first administrator:

| Variable | What to supply |
| --- | --- |
| `POSTBOX_ADMIN_EMAIL` | Your administrator email address |
| `POSTBOX_ADMIN_PASSWORD` | A unique password of at least 8 characters |
| `POSTBOX_ADMIN_TOTP` | The base32 secret enrolled in your authenticator, rather than a current six-digit code |

Create the account, remove the bootstrap secrets, and start the service:

```sh
./bin/postbox admin
unset POSTBOX_ADMIN_PASSWORD POSTBOX_ADMIN_TOTP
./bin/postbox serve --development --secure-cookies=false
```

Open <http://localhost:8080> and sign in with your password and authenticator code. These development flags allow local HTTP. A production instance requires an HTTPS origin and secure cookies.

> Keep the database and its generated `.secret` file together. The secret protects tracking links and encrypts saved AWS credentials. Losing it breaks previously sent links and prevents credentials from being decrypted.

## Connect a brand

A brand holds its sender identity, audience and AWS connection. Select or create the brand you will send from, then open **Settings > Amazon SES** as an administrator.

1. Follow [Create the API keys in IAM](aws.md#create-the-api-keys-in-iam) to create a dedicated user, attach the Postbox policies and retrieve the keys in the AWS Console. Enter the brand's AWS access key, secret key, optional session token and SES region, then select **Save Amazon SES settings**.
2. Check the connection and review its account status and sending quota.
3. Set the sender name, sender email and reply-to address in **Brand identity**.

Use AWS IAM access keys. SES SMTP credentials cannot be used here. Each brand needs saved keys; server environment credentials and instance roles do not supply them. Saved credentials are encrypted in SQLite and are never returned by the API.

SES identities and quotas belong to an AWS account and region. Use the same region for the brand's identity and event resources. Brands sharing an account and region share its sending pace. Increasing Postbox's worker limit does not increase the SES quota.

## Verify your sender domain

Open **Domains** for the selected brand. It starts with the domain from the sender email. You can enter a parent domain and select **View domain** instead.

1. Select **Verify domain** to create a missing identity with Easy DKIM.
2. Copy the displayed DNS record names and values into your DNS provider.
3. Select **Refresh status** to check verification, sending eligibility and DKIM signing.

Use the exact records returned by AWS. Postbox reads existing identities without changing their DKIM settings; BYODKIM keys and DEED parent setup are managed in AWS. Domain creation and MAIL FROM changes require an administrator. Brand readers can view status and records.

DNS detection can take up to 72 hours. If SES is still in the sandbox, request production access in AWS before sending to an ordinary audience.

For a custom return path, set a **MAIL FROM subdomain**, such as `bounce.example.com`, and select **Save MAIL FROM settings**. Publish the displayed MX record, including its priority, and SPF TXT record. Choose whether SES should fall back to its default MAIL FROM domain or reject sends while verification is pending. A blank subdomain disables custom MAIL FROM. Publish your sender domain's DMARC policy separately.

## Receive delivery events

Delivery events let Postbox reconcile accepted sends and process bounces and complaints. Configure them before sending a production campaign.

As an administrator, open **Settings > Amazon SES**:

1. Save the brand's region and AWS keys with the runtime and setup permissions from the [SES & SNS Setup](aws.md). Leave **SNS topic ARN** blank to create a topic, or save an existing Standard topic ARN from the same account and region.
2. Select **Set up SES and SNS**. Postbox creates or reuses the topic, adds its SES publish policy, creates or reuses the configuration set, configures its event destination and subscribes this deployment's HTTPS endpoint.
3. Review the returned configuration set, topic and endpoint. Select **Refresh event status** until the SNS subscription is confirmed and event delivery is ready.

If the brand has no configuration set name, setup generates and saves one. A name set in **Brand identity** is reused and created in AWS if missing. Setup can be run again to finish a partial attempt.

Set `POSTBOX_BASE_URL` to the deployment's public HTTPS origin before setup. SNS sends confirmation to `/events/sns`; Postbox verifies the message and confirms automatically. This route must be reachable from AWS without a login or proxy challenge, and Postbox needs outbound HTTPS access to AWS signing certificates and the signed confirmation URL. Local HTTP development origins cannot receive SNS subscriptions. DNS records are published separately at your DNS provider.

## Prepare the first campaign

Build a small audience with documented consent, create a campaign, and send a test to yourself. Review the sender domain, audience, content, unsubscribe links and SES quota in the campaign checklist. Follow [Sending newsletters](sending-newsletters.md) for the complete workflow.

When those checks are complete, enable sending in **Settings**. The switch is stored in the database and survives restarts. It also controls test and transactional email jobs, including confirmation emails. Missing credentials produce errors; the service never silently substitutes a test sender.
