CREATE TABLE campaign_lifecycle_events (
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  event TEXT NOT NULL CHECK (event IN ('campaign_started','campaign_finished')),
  emitted_at INTEGER NOT NULL,
  PRIMARY KEY (campaign_id,event)
) STRICT, WITHOUT ROWID;

-- Existing campaigns must not produce a second start after upgrading or resuming.
INSERT INTO campaign_lifecycle_events(campaign_id,event,emitted_at)
SELECT id,'campaign_started',started_at FROM campaigns WHERE started_at IS NOT NULL;
INSERT INTO campaign_lifecycle_events(campaign_id,event,emitted_at)
SELECT id,'campaign_finished',coalesce(finished_at,updated_at)
FROM campaigns WHERE status IN ('sent','cancelled');
