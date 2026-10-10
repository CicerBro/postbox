ALTER TABLE subscribers ADD COLUMN marketing_unsubscribed_at INTEGER;
ALTER TABLE subscription_confirmations ADD COLUMN resubscribe_brand INTEGER NOT NULL DEFAULT 0;

-- Explicit unsubscribe-all activity establishes a historical brand opt-out.
-- Older API activity used {} for both list and all unsubscribe. Conservatively
-- block those ambiguous records only when no membership remains active and no
-- later subscribe activity exists. This can over-block a historical list-only
-- opt-out, but cannot silently make a possible brand opt-out sendable.
-- Explicit list-only activity remains list-only.
UPDATE subscribers SET marketing_unsubscribed_at=(
 SELECT max(a.at) FROM subscriber_activity a
 WHERE a.subscriber_id=subscribers.id AND a.brand_id=subscribers.brand_id
 AND a.kind='unsubscribe' AND json_valid(a.detail)
 AND ((json_type(a.detail,'$.list_id')='integer' AND json_extract(a.detail,'$.list_id')=0) OR json_type(a.detail,'$.list_id') IS NULL)
) WHERE NOT EXISTS (SELECT 1 FROM list_subscribers ls WHERE ls.subscriber_id=subscribers.id AND ls.status<>'unsubscribed') AND EXISTS (
 SELECT 1 FROM subscriber_activity a
 WHERE a.subscriber_id=subscribers.id AND a.brand_id=subscribers.brand_id
 AND a.kind='unsubscribe' AND json_valid(a.detail)
 AND ((json_type(a.detail,'$.list_id')='integer' AND json_extract(a.detail,'$.list_id')=0) OR json_type(a.detail,'$.list_id') IS NULL)
 AND NOT EXISTS (SELECT 1 FROM subscriber_activity later WHERE later.subscriber_id=a.subscriber_id AND later.brand_id=a.brand_id AND later.kind='subscribe' AND later.at>=a.at)
);
