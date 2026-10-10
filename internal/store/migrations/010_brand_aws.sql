-- AWS material is authenticated and encrypted with a server-derived key.
CREATE TABLE brand_aws_settings (
  brand_id INTEGER PRIMARY KEY REFERENCES brands(id) ON DELETE CASCADE,
  mode TEXT NOT NULL CHECK(mode IN ('static','instance_role')),
  region TEXT NOT NULL,
  credentials BLOB NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
) STRICT;

-- Preserve legacy SES imports for existing brands without applying one
-- AWS account's suppression list globally to future brands.
INSERT OR IGNORE INTO suppressions(brand_id,kind,value,reason,created_at)
SELECT b.id,s.kind,s.value,s.reason,s.created_at
FROM brands b CROSS JOIN suppressions s
WHERE s.brand_id IS NULL AND s.reason='ses_sync';
DELETE FROM suppressions WHERE brand_id IS NULL AND reason='ses_sync';
