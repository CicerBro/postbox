CREATE INDEX campaign_recipients_message_retention
ON campaign_recipients(sent_at,campaign_id,subscriber_id)
WHERE message_id IS NOT NULL AND sent_at IS NOT NULL;

CREATE INDEX send_attempts_retention ON send_attempts(finished_at,id)
WHERE finished_at IS NOT NULL AND outcome<>'started';
