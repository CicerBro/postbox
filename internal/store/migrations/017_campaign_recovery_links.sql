ALTER TABLE campaigns ADD COLUMN recovery_of_campaign_id INTEGER
  REFERENCES campaigns(id) ON DELETE SET NULL;
CREATE INDEX campaigns_recovery_of ON campaigns(recovery_of_campaign_id);
