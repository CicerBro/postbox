-- Campaign UUIDs can be recreated by imports, so capabilities also bind to a
-- random identity independent of the public UUID and numeric database ID.
ALTER TABLE campaigns ADD COLUMN tracking_identity TEXT NOT NULL DEFAULT '' CHECK(length(tracking_identity) IN (0,32));
UPDATE campaigns SET tracking_identity=lower(hex(randomblob(16)));
CREATE UNIQUE INDEX campaigns_tracking_identity ON campaigns(tracking_identity) WHERE tracking_identity!='';
CREATE TRIGGER campaigns_tracking_identity_insert AFTER INSERT ON campaigns
WHEN NEW.tracking_identity=''
BEGIN
  UPDATE campaigns SET tracking_identity=lower(hex(randomblob(16))) WHERE id=NEW.id;
END;
DROP TRIGGER campaigns_tracking_identity_immutable;
CREATE TRIGGER campaigns_tracking_identity_immutable BEFORE UPDATE OF uuid,tracking_identity,tracking_legacy ON campaigns
WHEN NEW.uuid!=OLD.uuid OR (OLD.tracking_identity!='' AND NEW.tracking_identity!=OLD.tracking_identity) OR NEW.tracking_legacy!=OLD.tracking_legacy
BEGIN
  SELECT RAISE(ABORT,'campaign tracking identity is immutable');
END;
