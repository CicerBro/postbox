-- Drafts should never retain selections for deleted or foreign-brand audiences.
-- Terminal campaign targets remain available for historical reporting.
DELETE FROM campaign_targets
WHERE campaign_id IN (SELECT id FROM campaigns WHERE status = 'draft')
  AND (
    (target_type = 'list' AND NOT EXISTS (
      SELECT 1 FROM lists l JOIN campaigns c ON c.id = campaign_targets.campaign_id
      WHERE l.id = campaign_targets.target_id AND l.brand_id = c.brand_id
    )) OR
    (target_type = 'segment' AND NOT EXISTS (
      SELECT 1 FROM segments s JOIN campaigns c ON c.id = campaign_targets.campaign_id
      WHERE s.id = campaign_targets.target_id AND s.brand_id = c.brand_id
    ))
  );

-- Protect live audiences, including exclusions. Allow whole-brand cascading
-- deletion, where the brand row and its campaigns are being removed together.
CREATE TRIGGER lists_protect_active_campaigns BEFORE DELETE ON lists
WHEN EXISTS (SELECT 1 FROM brands WHERE id = OLD.brand_id)
  AND EXISTS (
    SELECT 1 FROM campaign_targets t JOIN campaigns c ON c.id = t.campaign_id
    WHERE t.target_type = 'list' AND t.target_id = OLD.id
      AND c.brand_id = OLD.brand_id
      AND c.status IN ('scheduled', 'preparing', 'sending', 'paused')
  )
BEGIN
  SELECT RAISE(ABORT, 'This list is used by a scheduled or active campaign. Cancel the campaign before deleting the list.');
END;

CREATE TRIGGER lists_remove_draft_targets AFTER DELETE ON lists
BEGIN
  DELETE FROM campaign_targets
  WHERE target_type = 'list' AND target_id = OLD.id
    AND campaign_id IN (SELECT id FROM campaigns WHERE brand_id = OLD.brand_id AND status = 'draft');
END;

CREATE TRIGGER segments_protect_active_campaigns BEFORE DELETE ON segments
WHEN EXISTS (SELECT 1 FROM brands WHERE id = OLD.brand_id)
  AND EXISTS (
    SELECT 1 FROM campaign_targets t JOIN campaigns c ON c.id = t.campaign_id
    WHERE t.target_type = 'segment' AND t.target_id = OLD.id
      AND c.brand_id = OLD.brand_id
      AND c.status IN ('scheduled', 'preparing', 'sending', 'paused')
  )
BEGIN
  SELECT RAISE(ABORT, 'This segment is used by a scheduled or active campaign. Cancel the campaign before deleting the segment.');
END;

CREATE TRIGGER segments_remove_draft_targets AFTER DELETE ON segments
BEGIN
  DELETE FROM campaign_targets
  WHERE target_type = 'segment' AND target_id = OLD.id
    AND campaign_id IN (SELECT id FROM campaigns WHERE brand_id = OLD.brand_id AND status = 'draft');
END;
