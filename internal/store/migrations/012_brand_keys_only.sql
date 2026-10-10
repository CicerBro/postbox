-- Remove legacy server-credential connections. Brands must provide their own keys.
DELETE FROM suppressions
WHERE reason='ses_sync'
  AND brand_id NOT IN (SELECT brand_id FROM brand_aws_settings WHERE mode='static');

CREATE TABLE brand_aws_settings_keys (
  brand_id INTEGER PRIMARY KEY REFERENCES brands(id) ON DELETE CASCADE,
  mode TEXT NOT NULL CHECK(mode='static'),
  region TEXT NOT NULL,
  credentials BLOB NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
) STRICT;
INSERT INTO brand_aws_settings_keys(brand_id,mode,region,credentials,revision,updated_at)
SELECT brand_id,mode,region,credentials,revision,updated_at
FROM brand_aws_settings WHERE mode='static';
DROP TABLE brand_aws_settings;
ALTER TABLE brand_aws_settings_keys RENAME TO brand_aws_settings;
