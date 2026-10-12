-- Preserve already-issued capabilities only for identities present at upgrade.
-- New rows receive random immutable identities even when SQLite reuses an ID.
ALTER TABLE subscribers ADD COLUMN tracking_identity TEXT NOT NULL DEFAULT '' CHECK(length(tracking_identity) IN (0,32));
ALTER TABLE subscribers ADD COLUMN tracking_legacy INTEGER NOT NULL DEFAULT 0 CHECK(tracking_legacy IN (0,1));
ALTER TABLE campaigns ADD COLUMN tracking_legacy INTEGER NOT NULL DEFAULT 0 CHECK(tracking_legacy IN (0,1));
UPDATE subscribers SET tracking_identity=lower(hex(randomblob(16))),tracking_legacy=1;
UPDATE campaigns SET tracking_legacy=1;
CREATE UNIQUE INDEX subscribers_tracking_identity ON subscribers(tracking_identity) WHERE tracking_identity!='';
CREATE TRIGGER subscribers_tracking_identity_insert AFTER INSERT ON subscribers
WHEN NEW.tracking_identity=''
BEGIN
  UPDATE subscribers SET tracking_identity=lower(hex(randomblob(16))) WHERE id=NEW.id;
END;
CREATE TRIGGER subscribers_tracking_identity_immutable BEFORE UPDATE OF tracking_identity,tracking_legacy ON subscribers
WHEN (OLD.tracking_identity!='' AND NEW.tracking_identity!=OLD.tracking_identity) OR NEW.tracking_legacy!=OLD.tracking_legacy
BEGIN
  SELECT RAISE(ABORT,'subscriber tracking identity is immutable');
END;
CREATE TRIGGER campaigns_tracking_identity_immutable BEFORE UPDATE OF uuid,tracking_legacy ON campaigns
WHEN NEW.uuid!=OLD.uuid OR NEW.tracking_legacy!=OLD.tracking_legacy
BEGIN
  SELECT RAISE(ABORT,'campaign tracking identity is immutable');
END;
