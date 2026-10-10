package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestBrandConsentMigrationInfersOnlyExplicitInactiveOptOut(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "014" {
			break
		}
		source, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(source)); err != nil {
			t.Fatal(entry.Name(), err)
		}
	}
	for _, q := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'B','','sender@example.org',1)`,
		`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'l','L','single',1)`,
		`INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'explicit@example.org',1,1),(2,1,'ambiguous@example.org',1,1),(3,1,'list@example.org',1,1),(4,1,'renewed@example.org',1,1),(5,1,'active@example.org',1,1)`,
		`INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(1,1,'unsubscribe',2,'{"list_id":0}'),(1,2,'unsubscribe',2,'{}'),(1,3,'unsubscribe',2,'{"list_id":1}'),(1,4,'unsubscribe',2,'{"list_id":0}'),(1,4,'subscribe',3,'{}'),(1,5,'unsubscribe',2,'{"list_id":0}')`,
		`INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,5,'subscribed',1,3)`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	source, err := migrations.ReadFile("migrations/014_brand_marketing_consent.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(source)); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 5; id++ {
		var at *int64
		if err = db.QueryRow(`SELECT marketing_unsubscribed_at FROM subscribers WHERE id=?`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if (at != nil) != (id == 1 || id == 2) {
			t.Fatalf("subscriber %d opt-out %v", id, at)
		}
	}
}
