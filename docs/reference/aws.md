# AWS permissions and event resources

Apply permissions to each brand's saved IAM keys, scoped to the intended account, region and resources. Replace `REGION`, `ACCOUNT` and `example.com` in the checked-in policies.

## IAM permissions

The [runtime policy](../../deploy/iam-runtime.json) supports sending, connection checks, identity status, account suppression import. Connection checks need `ses:GetAccount` and `ses:ListEmailIdentities`; domain status needs `ses:GetEmailIdentity`; suppression import needs `ses:ListSuppressedDestinations`. STS `GetCallerIdentity` determines the shared account identity without an additional allow statement.

The sender calls SES v2 `SendEmail` with raw MIME, explicit destinations, configuration set and brand/campaign tags. It does not use SMTP or SES templates. The policy also includes `ses:SendRawEmail` for compatibility.

The [setup policy](../../deploy/iam-setup.json) covers domain verification, custom MAIL FROM and creating the SES configuration set and configuring its event destination. Add a scoped identity ARN for every domain you intend to create or change. Existing keys need these permissions added before setup actions can work. Creating SNS topics and HTTPS subscriptions is a separate deployment step.

Only administrator sessions can change AWS settings. Brand members can check their brand's account status. Changing the connection clears only SES-imported suppressions and schedules a refresh; manual suppressions and subscriber bounce/complaint states remain.

## SNS delivery

Create a Standard SNS topic in the brand's SES region, save its ARN in Amazon SES settings, and subscribe your public `https://mail.example.com/events/sns` URL as an HTTPS endpoint. Set the configuration set name in Brand identity, then select **Create config set at AWS**. Postbox creates the configuration set if missing and creates or updates its `postbox` event destination.

The SNS topic policy allows SES from the intended account and configuration set:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "ses.amazonaws.com"},
    "Action": "sns:Publish",
    "Resource": "arn:aws:sns:REGION:ACCOUNT:postbox-events",
    "Condition": {
      "StringEquals": {"AWS:SourceAccount": "ACCOUNT"},
      "ArnLike": {"AWS:SourceArn": "arn:aws:ses:REGION:ACCOUNT:configuration-set/postbox"}
    }
  }]
}
```


## Processing and failure handling

Postbox returns success to SNS only after processing and committing the event. Processing failures return an error so SNS can retry delivery.

HTTPS SNS ingestion validates signatures, the expected topic and the signing certificate URL and chain before processing an event or confirming a subscription. Keep outbound access to the signing certificates available.

Events must resolve to an originating brand using its configured topic, event tags or an unambiguous stored message ID. Unscoped events are rejected. Hard bounces and complaints affect the originating brand; explicit administrator global suppressions remain global. Suppression sync runs through durable jobs.
