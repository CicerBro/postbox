# SES & SNS Setup

Apply permissions to each brand's saved IAM keys, scoped to its AWS account, region and resources. Replace `REGION`, `ACCOUNT`, `example.com` and `https://mail.example.com/events/sns` in the checked-in policies. Use both policies while setting up a brand. For GovCloud, replace the ARN partition `aws` with `aws-us-gov`.

## IAM permissions

The [runtime policy](../../deploy/iam-runtime.json) covers sending, connection checks, domain verification status, event status and account suppression import. The [setup policy](../../deploy/iam-setup.json) adds the permissions needed to create SES identities and event resources from the app.

| App action | IAM actions | Resource scope |
| --- | --- | --- |
| Send email | `ses:SendEmail`, `ses:SendRawEmail` | Verified identity and selected configuration set |
| Check account and quota | `ses:GetAccount`, `ses:ListEmailIdentities` | `*` because these APIs do not support resource scoping |
| Read domain status and DNS records | `ses:GetEmailIdentity` | Each sender domain identity |
| Verify a missing domain | `ses:CreateEmailIdentity` | Each sender domain identity |
| Set or clear custom MAIL FROM | `ses:PutEmailIdentityMailFromAttributes` | Each sender domain identity |
| Read event destination | `ses:GetConfigurationSetEventDestinations` | Selected configuration set |
| Create configuration set | `ses:CreateConfigurationSet` | Selected configuration set, including names that do not exist yet |
| Configure SES event destination | `ses:CreateConfigurationSetEventDestination`, `ses:UpdateConfigurationSetEventDestination` | Selected configuration set |
| Create Standard SNS topic | `sns:CreateTopic` | Postbox topic names, including names that do not exist yet |
| Read and merge SNS topic policy | `sns:GetTopicAttributes`, `sns:SetTopicAttributes` | Selected SNS topic |
| Subscribe this deployment | `sns:Subscribe` | Selected SNS topic, restricted to protocol `https` and the exact endpoint |
| Repair an existing subscription | `sns:SetSubscriptionAttributes` | Selected SNS topic; the saved keys must own the subscription |
| Read subscription confirmation status | `sns:ListSubscriptionsByTopic` | Selected SNS topic |
| Import account suppressions | `ses:ListSuppressedDestinations` | `*` |

Add an identity ARN for every domain you intend to create or change. Domain and MAIL FROM setup do not create DNS records at your DNS provider. They return the records AWS requires. BYODKIM and DEED parent configuration remain in AWS.

Generated configuration sets use `postbox-<deployment hash>-brand-<brand ID>` and topics use that name followed by `-events`. The examples allow the `postbox-*` prefix, plus the manually named `postbox` configuration set and `postbox-events` topic. Replace those prefix patterns with exact ARNs after provisioning if desired. For existing resources with other names, add their exact ARNs to both policies. Identity ARNs use `arn:aws:ses:REGION:ACCOUNT:identity/example.com`; configuration sets use `configuration-set/NAME`; SNS topics use `arn:aws:sns:REGION:ACCOUNT:NAME`.

Existing keys need the setup grants before the buttons can work. You may remove creation and policy-management grants after setup; keep the runtime event-read permissions for **Refresh event status**. Restore the setup grants before rerunning setup or changing MAIL FROM. Do not grant `sns:*` or `ses:*` for this workflow.

STS `GetCallerIdentity` determines the saved connection's account without an additional allow statement. Setup creates untagged Standard topics and plain HTTPS subscriptions, so it needs no `sns:TagResource`, `iam:PassRole`, SNS platform, SMS or SQS permissions. Postbox confirms subscriptions using the token-bearing URL from a verified SNS message, so the saved IAM keys need no `sns:ConfirmSubscription` grant. SES publishes under its service principal through the topic policy; Postbox's keys need no `sns:Publish` grant.

The sender uses SES v2 `SendEmail` with raw MIME, explicit destinations, configuration set and brand/campaign tags. It does not use SMTP or SES templates. The policy includes `ses:SendRawEmail` for compatibility.

Only administrator sessions can create resources or change AWS settings. Brand readers can view verification and event status. Changing the connection clears only SES-imported suppressions and schedules a refresh; manual suppressions and subscriber bounce/complaint states remain.

For AWS action and resource definitions, see the [SNS authorization reference](https://docs.aws.amazon.com/service-authorization/latest/reference/list_sns.html) and [SES v2 authorization reference](https://docs.aws.amazon.com/service-authorization/latest/reference/list_sesv2.html).

## Automatic SES and SNS setup

In **Settings > Amazon SES**, save the connection, then select **Set up SES and SNS**. A blank topic ARN creates a Standard topic in the saved SES region. A saved topic is reused and must belong to that AWS account and region. A blank configuration set generates one and saves it to the brand; a saved name is reused and created in SES if missing.

Setup preserves unrelated topic-policy statements and adds or updates its own statement allowing only SES from this account and configuration set to publish. Its permission follows AWS's [SES event destination policy](https://docs.aws.amazon.com/ses/latest/dg/event-publishing-add-event-destination-sns.html):

```json
{
  "Effect": "Allow",
  "Principal": {"Service": "ses.amazonaws.com"},
  "Action": "sns:Publish",
  "Resource": "arn:aws:sns:REGION:ACCOUNT:TOPIC",
  "Condition": {
    "StringEquals": {
      "AWS:SourceAccount": "ACCOUNT",
      "AWS:SourceArn": "arn:aws:ses:REGION:ACCOUNT:configuration-set/NAME"
    }
  }
}
```

Postbox creates or updates the `postbox` SES destination for send, delivery, bounce, complaint, reject, rendering-failure and delivery-delay events. It subscribes `POSTBOX_BASE_URL/events/sns` using HTTPS and signed SNS envelopes, with raw message delivery disabled. Confirmed subscriptions to that endpoint are reused; rerunning setup clears their filter policy so all configured events arrive. Other subscriptions and event destinations are preserved.

SNS confirmation is asynchronous. **Refresh event status** reads AWS and distinguishes an absent subscription, pending confirmation and a confirmed subscription. A pending response means the resources were configured, but the HTTPS callback has not yet been confirmed. Ensure the endpoint is public and reachable from AWS, its certificate is trusted, the proxy allows `/events/sns` without authentication, and outbound access to AWS signing certificates and the confirmation URL works. Then refresh or rerun setup to request confirmation again.

Partial setup is retained if an AWS call fails. The error names the stage and permissions to check. Correct the grants or connectivity and rerun setup; it reuses the saved topic and configuration set. Setup never deletes AWS resources. If the public origin changes, setup subscribes the new endpoint; remove obsolete subscriptions in AWS after the new one is confirmed. Topic policy management reads and merges the existing policy, so avoid editing it concurrently in another tool.

## Processing and failure handling

Postbox returns success to SNS only after processing and committing the event. Processing failures return an error so SNS can retry delivery.

HTTPS SNS ingestion validates signatures, the expected topic and the signing certificate URL and chain before processing an event or confirming a subscription. Keep outbound access to the signing certificates available.

Events must resolve to an originating brand using its configured topic, event tags or an unambiguous stored message ID. Unscoped events are rejected. Hard bounces and complaints affect the originating brand; explicit administrator global suppressions remain global. Suppression sync runs through durable jobs.
