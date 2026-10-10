-- These private fingerprints deliberately have no subscriber foreign key.
CREATE TABLE campaign_dispatch_ledger (
 campaign_id INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
 email_hash BLOB NOT NULL CHECK(length(email_hash)=32),
 outcome TEXT NOT NULL CHECK(outcome IN ('started','sent','unknown','rejected')),
 generation INTEGER NOT NULL CHECK(generation>0),
 owner_subscriber_id INTEGER NOT NULL,
 owner_attempt INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(campaign_id,email_hash)
) STRICT, WITHOUT ROWID;
CREATE INDEX campaign_dispatch_owner ON campaign_dispatch_ledger(campaign_id,owner_subscriber_id,owner_attempt);
ALTER TABLE send_attempts ADD COLUMN dispatch_generation INTEGER NOT NULL DEFAULT 0;
INSERT INTO settings(key,value) VALUES('dispatch_ledger_bootstrap','0:0');
