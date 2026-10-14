-- Preserve timestamped counters so reports can choose arbitrary local day boundaries.
CREATE TABLE list_active_history (
  list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  at INTEGER NOT NULL,
  active INTEGER NOT NULL,
  subscribed INTEGER NOT NULL DEFAULT 0,
  unsubscribed INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(list_id,at)
) STRICT, WITHOUT ROWID;
INSERT INTO list_active_history(list_id,at,active)
SELECT list_id,cast(unixepoch('subsec')*1000 AS INTEGER),active FROM list_counters;

CREATE TABLE send_activity (
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  at INTEGER NOT NULL,
  sent INTEGER NOT NULL,
  PRIMARY KEY(brand_id,at)
) STRICT, WITHOUT ROWID;
-- Today and yesterday remain within recipient retention. Recover minute counters without scanning retained history on each request.
INSERT INTO send_activity(brand_id,at,sent)
SELECT c.brand_id,r.sent_at/60000*60000,count(*) FROM campaign_recipients r
JOIN campaigns c ON c.id=r.campaign_id
WHERE r.sent_at>=cast(unixepoch('subsec')*1000 AS INTEGER)-259200000
AND r.message_id IS NOT NULL
GROUP BY c.brand_id,r.sent_at/60000*60000;

CREATE TRIGGER send_activity_acceptance AFTER UPDATE OF status,sent_at ON campaign_recipients
WHEN NEW.status='sent' AND NEW.sent_at IS NOT NULL AND OLD.status!='sent'
BEGIN
  INSERT INTO send_activity(brand_id,at,sent)
  SELECT brand_id,NEW.sent_at/60000*60000,1 FROM campaigns WHERE id=NEW.campaign_id
  ON CONFLICT(brand_id,at) DO UPDATE SET sent=sent+1;
END;
