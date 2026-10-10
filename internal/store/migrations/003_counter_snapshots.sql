CREATE TABLE list_counter_snapshots (
 list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
 day INTEGER NOT NULL,
 subscribed INTEGER NOT NULL DEFAULT 0,
 unsubscribed INTEGER NOT NULL DEFAULT 0,
 bounced INTEGER NOT NULL DEFAULT 0,
 complained INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(list_id,day)
) STRICT, WITHOUT ROWID;
CREATE INDEX jobs_failed ON jobs(id) WHERE status='failed';
