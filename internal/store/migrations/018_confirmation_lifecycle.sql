ALTER TABLE list_subscribers ADD COLUMN confirmation_started_at INTEGER;
UPDATE list_subscribers SET confirmation_started_at=updated_at WHERE status='unconfirmed';
CREATE INDEX list_subscribers_unconfirmed_started ON list_subscribers(COALESCE(confirmation_started_at,updated_at)) WHERE status='unconfirmed';
