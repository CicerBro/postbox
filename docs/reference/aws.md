# AWS permissions and event resources

Apply permissions to each brand's saved IAM keys, scoped to the intended account, region and resources. Replace `REGION`, `ACCOUNT` and `example.com` in the checked-in policies.

## IAM permissions

The [runtime policy](../../deploy/iam-runtime.json) supports sending, connection checks, identity status, account suppression import and queue polling. Connection checks need `ses:GetAccount` and `ses:ListEmailIdentities`; domain status needs `ses:GetEmailIdentity`; suppression import needs `ses:ListSuppressedDestinations`. Restrict SQS access to the brand's event queue. STS `GetCallerIdentity` determines the shared account identity without an additional allow statement.

The sender calls SES v2 `SendEmail` with raw MIME, explicit destinations, configuration set and brand/campaign tags. It does not use SMTP or SES templates. The policy also includes `ses:SendRawEmail` for compatibility.

The [setup policy](../../deploy/iam-setup.json) covers domain verification, custom MAIL FROM and configuring the SES event destination. Add a scoped identity ARN for every domain you intend to create or change. Existing keys need these permissions added before setup actions can work. Creating SNS/SQS resources is a separate deployment step.

Only administrator sessions can change AWS settings. Brand members can check their brand's account status. Changing the connection clears only SES-imported suppressions and schedules a refresh; manual suppressions and subscriber bounce/complaint states remain.

## SNS and SQS policies

Create a topic, event queue and dead-letter queue in the brand's SES region. Set message retention, dead-letter alarms and a redrive maximum, for example five receives. Subscribe the queue ARN to the topic with raw message delivery disabled.

The queue policy allows only the configured topic to publish:


```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "sns.amazonaws.com"},
    "Action": "sqs:SendMessage",
    "Resource": "arn:aws:sqs:REGION:ACCOUNT:postbox-events",
    "Condition": {"ArnEquals": {"aws:SourceArn": "arn:aws:sns:REGION:ACCOUNT:postbox-events"}}
  }]
}
```

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

Postbox validates the configured topic and deletes an SQS message only after its database transaction commits. Polling uses a 20-second long poll and a longer HTTP timeout. Poison messages remain for redrive; inspect failed messages and the dead-letter queue before replaying them.

HTTPS SNS ingestion validates signatures, the expected topic and the signing certificate URL and chain before processing an event or confirming a subscription. Keep outbound access to the signing certificates available.

Events must resolve to an originating brand using its configured topic/queue, event tags or an unambiguous stored message ID. Unscoped events are rejected. Hard bounces and complaints affect the originating brand; explicit administrator global suppressions remain global. Suppression sync runs through durable jobs.
