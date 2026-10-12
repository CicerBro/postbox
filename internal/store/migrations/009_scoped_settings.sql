-- Scoped settings belong to their owner and disappear with it. Match canonical
-- owner prefixes so malformed IDs cannot accidentally attach to a real owner.
CREATE TABLE brand_settings (
  brand_id INTEGER NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK(name != ''),
  value TEXT NOT NULL,
  PRIMARY KEY(brand_id,name)
) STRICT, WITHOUT ROWID;

CREATE TABLE list_settings (
  list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK(name != ''),
  value TEXT NOT NULL,
  PRIMARY KEY(list_id,name)
) STRICT, WITHOUT ROWID;

INSERT INTO brand_settings(brand_id,name,value)
SELECT b.id,substr(s.key,length('brand.' || b.id || '.')+1),s.value
FROM settings s JOIN brands b
  ON substr(s.key,1,length('brand.' || b.id || '.')) = 'brand.' || b.id || '.'
WHERE length(s.key) > length('brand.' || b.id || '.');

INSERT INTO list_settings(list_id,name,value)
SELECT l.id,substr(s.key,length('list.' || l.id || '.')+1),s.value
FROM settings s JOIN lists l
  ON substr(s.key,1,length('list.' || l.id || '.')) = 'list.' || l.id || '.'
WHERE length(s.key) > length('list.' || l.id || '.');

-- Orphaned and malformed scoped keys must never reach a replacement owner.
DELETE FROM settings WHERE key GLOB 'brand.*' OR key GLOB 'list.*';
