-- Keep migration provenance in migration_identities, out of public URLs.
UPDATE lists SET uuid = substr(uuid, 7)
WHERE uuid GLOB 'sendy-*' AND length(uuid) = 38
  AND substr(uuid, 7) NOT GLOB '*[^0-9a-f]*';

UPDATE campaigns SET uuid = substr(uuid, 7)
WHERE uuid GLOB 'sendy-*' AND length(uuid) = 38
  AND substr(uuid, 7) NOT GLOB '*[^0-9a-f]*';
