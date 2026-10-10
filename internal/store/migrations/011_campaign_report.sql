-- Bounded report samples use durable recipient timestamps after raw event retention.
CREATE INDEX campaign_recipients_recent_opened ON campaign_recipients(campaign_id,first_opened_at DESC,subscriber_id DESC) WHERE first_opened_at IS NOT NULL;
CREATE INDEX campaign_recipients_recent_unsubscribed ON campaign_recipients(campaign_id,unsubscribed_at DESC,subscriber_id DESC) WHERE unsubscribed_at IS NOT NULL;
CREATE INDEX campaign_recipients_recent_bounced ON campaign_recipients(campaign_id,bounced_at DESC,subscriber_id DESC) WHERE bounced_at IS NOT NULL;
CREATE INDEX campaign_recipients_recent_complained ON campaign_recipients(campaign_id,complained_at DESC,subscriber_id DESC) WHERE complained_at IS NOT NULL;
CREATE INDEX campaign_recipients_sent_at ON campaign_recipients(campaign_id,sent_at,subscriber_id) WHERE sent_at IS NOT NULL;
