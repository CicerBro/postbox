ALTER TABLE campaign_recipients ADD COLUMN bounce_kind TEXT NOT NULL DEFAULT ''
  CHECK (bounce_kind IN ('', 'hard', 'soft'));

-- Use previously matched events, leaving imported campaign totals and
-- historical recipients whose bounce details have expired intact.
CREATE TEMP TABLE campaign_bounce_backfill AS
WITH bounces AS (
  SELECT r.campaign_id, r.subscriber_id, r.delivered_at,
    a.at, CASE WHEN json_extract(a.detail, '$.subtype')='Permanent' THEN 'hard' ELSE 'soft' END AS bounce_kind
  FROM campaign_recipients r
  JOIN subscriber_activity a ON a.subscriber_id=r.subscriber_id
    AND a.kind='bounce' AND json_extract(a.detail, '$.campaign_id')=r.campaign_id
  WHERE r.bounced_at IS NOT NULL
), ranked AS (
  SELECT campaign_id,subscriber_id,delivered_at,at,bounce_kind,
    row_number() OVER (
      PARTITION BY campaign_id,subscriber_id ORDER BY at DESC,bounce_kind ASC
    ) AS latest,
    count(*) FILTER (WHERE bounce_kind='hard') OVER (PARTITION BY campaign_id,subscriber_id) AS hard,
    count(*) FILTER (WHERE bounce_kind='soft') OVER (PARTITION BY campaign_id,subscriber_id) AS soft
  FROM bounces
), outcomes AS (
  SELECT r.campaign_id,r.subscriber_id,r.at,r.bounce_kind,r.hard,r.soft,
    coalesce((
      SELECT max(a.at)
      FROM subscriber_activity a
      WHERE a.subscriber_id=r.subscriber_id AND a.kind='delivery'
        AND json_extract(a.detail, '$.campaign_id')=r.campaign_id
        AND a.at>r.delivered_at
    ),r.delivered_at) AS delivered_at
  FROM ranked r
  WHERE r.latest=1
)
SELECT campaign_id,subscriber_id,delivered_at,
  CASE WHEN delivered_at IS NULL OR at>=delivered_at THEN at END AS bounced_at,
  CASE WHEN delivered_at IS NULL OR at>=delivered_at THEN bounce_kind ELSE '' END AS bounce_kind,
  hard,soft
FROM outcomes;

CREATE UNIQUE INDEX campaign_bounce_backfill_recipient
  ON campaign_bounce_backfill(campaign_id,subscriber_id);

UPDATE campaign_recipients AS r
SET delivered_at=b.delivered_at,bounced_at=b.bounced_at,bounce_kind=b.bounce_kind
FROM campaign_bounce_backfill b
WHERE r.campaign_id=b.campaign_id AND r.subscriber_id=b.subscriber_id;

WITH corrections AS (
  SELECT campaign_id,
    sum(hard-(bounce_kind='hard')) AS hard,
    sum(soft-(bounce_kind='soft')) AS soft
  FROM campaign_bounce_backfill
  GROUP BY campaign_id
)
UPDATE campaign_stats AS s
SET bounces_hard=max(0,s.bounces_hard-c.hard),
    bounces_soft=max(0,s.bounces_soft-c.soft)
FROM corrections c
WHERE s.campaign_id=c.campaign_id;

DROP TABLE campaign_bounce_backfill;
