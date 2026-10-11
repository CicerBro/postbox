CREATE TABLE recurring_jobs (
    queue TEXT PRIMARY KEY,
    interval_ms INTEGER NOT NULL CHECK(interval_ms > 0),
    next_run_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX jobs_active_queue ON jobs(queue,run_at) WHERE status IN ('pending','running');
