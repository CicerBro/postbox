package store

import (
	"context"
	"testing"
)

func TestImportedPublicIDsMigration(t *testing.T) {
	db := testDB(t)
	_, err := db.Writer.Exec(`
DELETE FROM schema_migrations WHERE name='007_imported_public_ids.sql';
-- Simulate the pre-007 boundary, before tracking identities become immutable.
DROP TRIGGER campaigns_tracking_identity_immutable;
INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',1);
INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES
(1,1,'sendy-03b98c7c38d165c43fffc1be96d286d2','Imported','single',1),
(2,1,'local-token','Local','single',1);
INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES
(1,1,'sendy-03b98c7c38d165c43fffc1be96d286d2','sent','Imported','<p>History</p>',1,1),
(2,1,'local-token','draft','Local','<p>Local</p>',1,1);
INSERT INTO migration_identities(source,entity,source_id,target_id) VALUES
('source','lists','10',1),('source','campaigns','20',1);
`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = db.migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"lists", "campaigns"} {
			for id, want := range map[int]string{1: "03b98c7c38d165c43fffc1be96d286d2", 2: "local-token"} {
				var got string
				if err = db.Reader.QueryRow("SELECT uuid FROM "+table+" WHERE id=?", id).Scan(&got); err != nil || got != want {
					t.Fatalf("%s %d ID %q, want %q: %v", table, id, got, want, err)
				}
			}
		}
	}
	var mappings int
	if err = db.Reader.QueryRow("SELECT count(*) FROM migration_identities WHERE target_id=1").Scan(&mappings); err != nil || mappings != 2 {
		t.Fatalf("migration identities changed: count %d, err %v", mappings, err)
	}
}
