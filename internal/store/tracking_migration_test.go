package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const expandedTrackingMigrationFixture = `-- Preserve already-issued capabilities only for identities present at upgrade.
-- New rows receive random immutable identities even when SQLite reuses an ID.
ALTER TABLE subscribers ADD COLUMN tracking_identity TEXT NOT NULL DEFAULT '' CHECK(length(tracking_identity) IN (0,32));
ALTER TABLE subscribers ADD COLUMN tracking_legacy INTEGER NOT NULL DEFAULT 0 CHECK(tracking_legacy IN (0,1));
ALTER TABLE campaigns ADD COLUMN tracking_identity TEXT NOT NULL DEFAULT '' CHECK(length(tracking_identity) IN (0,32));
ALTER TABLE campaigns ADD COLUMN tracking_legacy INTEGER NOT NULL DEFAULT 0 CHECK(tracking_legacy IN (0,1));
UPDATE subscribers SET tracking_identity=lower(hex(randomblob(16))),tracking_legacy=1;
UPDATE campaigns SET tracking_identity=lower(hex(randomblob(16))),tracking_legacy=1;
CREATE UNIQUE INDEX campaigns_tracking_identity ON campaigns(tracking_identity) WHERE tracking_identity!='';
CREATE TRIGGER campaigns_tracking_identity_insert AFTER INSERT ON campaigns
WHEN NEW.tracking_identity=''
BEGIN
  UPDATE campaigns SET tracking_identity=lower(hex(randomblob(16))) WHERE id=NEW.id;
END;
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
CREATE TRIGGER campaigns_tracking_identity_immutable BEFORE UPDATE OF uuid,tracking_identity,tracking_legacy ON campaigns
WHEN NEW.uuid!=OLD.uuid OR (OLD.tracking_identity!='' AND NEW.tracking_identity!=OLD.tracking_identity) OR NEW.tracking_legacy!=OLD.tracking_legacy
BEGIN
  SELECT RAISE(ABORT,'campaign tracking identity is immutable');
END;
`

// Seed a real database exactly before the forward 012 upgrade, including the
// historically applied 011 bytes rather than manufacturing their schema record.
func trackingUpgradeFixture(t *testing.T, expanded bool, checksumOverride, changeSchema string) (string, map[int]string, map[int]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err = db.Exec(`PRAGMA foreign_keys=ON;CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL) STRICT,WITHOUT ROWID;`); err != nil {
		t.Fatal(err)
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() >= "011_tracking_identity.sql" {
			continue
		}
		source, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(source)); err != nil {
			t.Fatalf("seed %s: %v", file.Name(), err)
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations(name,checksum,applied_at) VALUES(?,?,100)`, file.Name(), fmt.Sprintf("%x", sha256.Sum256(source))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','','sender@example.org',1);INSERT INTO subscribers(id,brand_id,email,name,created_at,updated_at) VALUES(1,1,'original@example.org','Original profile',1,1);INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'original-public-id','sent','Original subject','Original HTML',1,1);`); err != nil {
		t.Fatal(err)
	}
	source, err := migrations.ReadFile("migrations/011_tracking_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	expectedChecksum := originalTrackingIdentityChecksum
	if expanded {
		source = []byte(expandedTrackingMigrationFixture)
		expectedChecksum = expandedTrackingIdentityChecksum
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != expectedChecksum {
		t.Fatalf("historical fixture bytes changed: %s", got)
	}
	if _, err = db.Exec(string(source)); err != nil {
		t.Fatal(err)
	}
	if checksumOverride != "" {
		expectedChecksum = checksumOverride
	}
	if _, err = db.Exec(`INSERT INTO schema_migrations(name,checksum,applied_at) VALUES('011_tracking_identity.sql',?,100)`, expectedChecksum); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO subscribers(id,brand_id,email,name,created_at,updated_at) VALUES(2,1,'new@example.org','New profile',1,1);INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(2,1,'new-public-id','draft','New subject','New HTML',1,1);`); err != nil {
		t.Fatal(err)
	}
	subscriberIdentities := map[int]string{}
	campaignIdentities := map[int]string{}
	for _, id := range []int{1, 2} {
		var identity string
		if err := db.QueryRow(`SELECT tracking_identity FROM subscribers WHERE id=?`, id).Scan(&identity); err != nil {
			t.Fatal(err)
		}
		subscriberIdentities[id] = identity
		if expanded {
			if err := db.QueryRow(`SELECT tracking_identity FROM campaigns WHERE id=?`, id).Scan(&identity); err != nil {
				t.Fatal(err)
			}
			campaignIdentities[id] = identity
		}
	}
	if changeSchema != "" {
		if _, err = db.Exec(changeSchema); err != nil {
			t.Fatal(err)
		}
	}
	return path, subscriberIdentities, campaignIdentities
}

func TestTrackingIdentityForwardUpgradePreservesAppliedHistory(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(map[bool]string{false: "original011", true: "expanded011"}[expanded], func(t *testing.T) {
			path, subscriberIdentities, campaignIdentities := trackingUpgradeFixture(t, expanded, "", "")
			for range 2 {
				db, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range []int{1, 2} {
					var subscriberIdentity, campaignIdentity, name, uuid, subject, body string
					var subscriberLegacy, campaignLegacy bool
					if err = db.Reader.QueryRow(`SELECT s.tracking_identity,c.tracking_identity,s.tracking_legacy,c.tracking_legacy,s.name,c.uuid,c.subject,c.html FROM subscribers s,campaigns c WHERE s.id=? AND c.id=?`, id, id).Scan(&subscriberIdentity, &campaignIdentity, &subscriberLegacy, &campaignLegacy, &name, &uuid, &subject, &body); err != nil {
						t.Fatal(err)
					}
					if subscriberIdentity != subscriberIdentities[id] || len(campaignIdentity) != 32 || subscriberLegacy != (id == 1) || campaignLegacy != (id == 1) {
						t.Fatalf("upgrade rotated identity or legacy scope for %d", id)
					}
					if prior, exists := campaignIdentities[id]; exists && campaignIdentity != prior {
						t.Fatalf("upgrade rotated existing campaign identity for %d", id)
					}
					campaignIdentities[id] = campaignIdentity
					prefix := map[int]string{1: "Original", 2: "New"}[id]
					publicPrefix := strings.ToLower(prefix)
					if name != prefix+" profile" || uuid != publicPrefix+"-public-id" || subject != prefix+" subject" || body != prefix+" HTML" {
						t.Fatal("upgrade changed application data")
					}
				}
				var checksum string
				var applied int64
				if err = db.Reader.QueryRow(`SELECT checksum,applied_at FROM schema_migrations WHERE name='011_tracking_identity.sql'`).Scan(&checksum, &applied); err != nil {
					t.Fatal(err)
				}
				want := originalTrackingIdentityChecksum
				if expanded {
					want = expandedTrackingIdentityChecksum
				}
				if checksum != want || applied != 100 {
					t.Fatal("upgrade rewrote applied 011 history", checksum, applied)
				}
				source, err := migrations.ReadFile("migrations/012_campaign_tracking_identity.sql")
				if err != nil {
					t.Fatal(err)
				}
				if err = db.Reader.QueryRow(`SELECT checksum FROM schema_migrations WHERE name='012_campaign_tracking_identity.sql'`).Scan(&checksum); err != nil || checksum != fmt.Sprintf("%x", sha256.Sum256(source)) {
					t.Fatal("canonical 012 history missing", checksum, err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestTrackingIdentityUpgradeRejectsUnknownChecksum(t *testing.T) {
	path, _, _ := trackingUpgradeFixture(t, false, strings.Repeat("a", 64), "")
	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Fatal("unrecognized 011 checksum accepted")
	}
	if !strings.Contains(err.Error(), "migration 011_tracking_identity.sql checksum changed") {
		t.Fatal(err)
	}
}

func TestExpandedTrackingUpgradeRequiresCompleteSchema(t *testing.T) {
	immutable := expandedTrackingMigrationFixture[strings.Index(expandedTrackingMigrationFixture, "CREATE TRIGGER campaigns_tracking_identity_immutable"):]
	for _, schemaChange := range []string{`DROP TRIGGER campaigns_tracking_identity_immutable`, `DROP TRIGGER campaigns_tracking_identity_insert`, `DROP INDEX campaigns_tracking_identity`, `DROP TRIGGER campaigns_tracking_identity_immutable;UPDATE campaigns SET tracking_identity='' WHERE id=1;` + immutable, `DROP TRIGGER campaigns_tracking_identity_immutable;CREATE TRIGGER campaigns_tracking_identity_immutable BEFORE UPDATE ON campaigns BEGIN SELECT 1;END;`} {
		t.Run(schemaChange, func(t *testing.T) {
			path, _, _ := trackingUpgradeFixture(t, true, "", schemaChange)
			db, err := Open(path)
			if err == nil {
				db.Close()
				t.Fatal("incomplete expanded schema accepted")
			}
			if !strings.Contains(err.Error(), "expanded schema") {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var count int
			if err = raw.QueryRow(`SELECT count(*) FROM schema_migrations WHERE name='012_campaign_tracking_identity.sql'`).Scan(&count); err != nil || count != 0 {
				t.Fatal("rejected upgrade recorded 012", count, err)
			}
		})
	}
}
