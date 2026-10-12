ALTER TABLE jobs ADD COLUMN failed_at INTEGER;
-- The original failure time is unknown. Give existing failures a full retention
-- period from upgrade rather than expiring them based on their enqueue time.
UPDATE jobs SET failed_at=CAST(strftime('%s','now') AS INTEGER)*1000 WHERE status='failed';
CREATE INDEX jobs_failed_retention ON jobs(failed_at,id) WHERE status='failed';
