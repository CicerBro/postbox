-- Imported Sendy campaigns are historical records. Keep local cancellations intact.
-- This only sets terminal metadata and does not enqueue jobs or recipients.
UPDATE campaigns
SET status = 'sent', finished_at = coalesce(finished_at, updated_at)
WHERE status IN ('sent', 'cancelled')
  AND id IN (SELECT target_id FROM migration_identities WHERE entity = 'campaigns');
