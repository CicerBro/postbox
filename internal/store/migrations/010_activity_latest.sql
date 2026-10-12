-- Cover the per-subscriber latest-activity lookup, including its timestamp order.
CREATE INDEX subscriber_activity_latest ON subscriber_activity(brand_id,subscriber_id,at DESC,id DESC);
