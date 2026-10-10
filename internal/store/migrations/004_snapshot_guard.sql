ALTER TABLE campaign_preparations ADD COLUMN recipients_snapshotted INTEGER NOT NULL DEFAULT 0 CHECK(recipients_snapshotted IN (0,1));
