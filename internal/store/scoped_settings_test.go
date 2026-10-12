package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestScopedSettingsMigrationPreservesOwnersAndDropsOrphans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedDeletionAudiences(t, db, "draft")
	for _, query := range []string{
		"DROP TABLE brand_settings", "DROP TABLE list_settings",
		"DELETE FROM schema_migrations WHERE name='009_scoped_settings.sql'",
		`INSERT INTO settings(key,value) VALUES ('brand.1.goodbye_html','Private brand page'),('list.1.confirmation_subject','Private list subject'),('brand.99.goodbye_html','Orphan'),('list.99.goodbye_html','Orphan'),('brand.01.goodbye_html','Malformed'),('brand.1x.goodbye_html','Malformed'),('brand.1.','Empty name'),('global_custom','Preserved')`,
	} {
		if _, err = db.Writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if value, err := db.BrandSetting(ctx, 1, "goodbye_html"); err != nil || value != "Private brand page" {
		t.Fatalf("brand migration: %q %v", value, err)
	}
	if value, err := db.ListSetting(ctx, 1, "confirmation_subject"); err != nil || value != "Private list subject" {
		t.Fatalf("list migration: %q %v", value, err)
	}
	if value, err := db.Setting(ctx, "global_custom"); err != nil || value != "Preserved" {
		t.Fatalf("global migration: %q %v", value, err)
	}
	var scoped int
	if err = db.Reader.QueryRow("SELECT count(*) FROM settings WHERE key GLOB 'brand.*' OR key GLOB 'list.*'").Scan(&scoped); err != nil || scoped != 0 {
		t.Fatalf("legacy scoped keys remain: %d %v", scoped, err)
	}
	if err = db.SetBrandSetting(ctx, 99, "goodbye_html", "Orphan"); err == nil {
		t.Fatal("nonexistent brand accepted")
	}
	if err = db.SetListSetting(ctx, 99, "goodbye_html", "Orphan"); err == nil {
		t.Fatal("nonexistent list accepted")
	}
}

func TestOwnerDeletionRemovesScopedSettingsAndQueuedWork(t *testing.T) {
	for _, owner := range []string{"brand", "list"} {
		t.Run(owner, func(t *testing.T) {
			db := testDB(t)
			seedDeletionAudiences(t, db, "draft")
			ctx := context.Background()
			if err := db.SetBrandSetting(ctx, 1, "goodbye_html", "Private brand page"); err != nil {
				t.Fatal(err)
			}
			if err := db.SetListSetting(ctx, 1, "goodbye_html", "Private list page"); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{
				`INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('transactional_email','{"brand_id":1,"list_id":1}','pending',0,0),('outbound_webhook','{"webhook_id":1,"body":{"brand_id":1,"data":{"list_id":1}}}','pending',0,0),('maintenance','{}','pending',0,0)`,
				`INSERT INTO opens(campaign_id,subscriber_id,at) VALUES(1,1,0)`,
				`INSERT INTO clicks(campaign_id,subscriber_id,link_id,at) VALUES(1,1,1,0)`,
				`INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at) VALUES(1,1,'upsert',0)`,
			} {
				if _, err := db.Writer.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if owner == "brand" {
				if _, err = db.Writer.Exec(`INSERT INTO webhooks(id,brand_id,url,events,secret) VALUES(1,1,'https://example.org/hook','["subscribe"]','secret'); INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('outbound_webhook','{"webhook_id":1}','pending',0,0)`); err != nil {
					t.Fatal(err)
				}
				err = db.DeleteBrand(ctx, 1)
			} else {
				err = db.DeleteList(ctx, 1, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Explicitly reuse the deleted numeric ID. Ownership must still protect
			// replacement records, independently of immutable tracking identities.
			if owner == "brand" {
				if _, err = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Replacement','Sender','new@example.com',0)`); err != nil {
					t.Fatal(err)
				}
				if _, err = db.BrandSetting(ctx, 1, "goodbye_html"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("brand leaked private setting: %v", err)
				}
				for _, table := range []string{"opens", "clicks", "subscriber_activity"} {
					var n int
					if err = db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
						t.Fatalf("%s still contains owner data: %d %v", table, n, err)
					}
				}
			}
			if _, err = db.Writer.Exec(`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'replacement','Replacement','single',0)`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ListSetting(ctx, 1, "goodbye_html"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("list leaked private setting: %v", err)
			}
			var jobs int
			if err = db.Reader.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 1 {
				t.Fatalf("queued owner notifications survived or maintenance removed: %d %v", jobs, err)
			}
		})
	}
}

func TestDeleteListProtectionRollsBackLifecycleChanges(t *testing.T) {
	db := testDB(t)
	seedDeletionAudiences(t, db, "sending")
	ctx := context.Background()
	if err := db.SetListSetting(ctx, 1, "goodbye_html", "Still owned"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('transactional_email','{"brand_id":1,"list_id":1}','pending',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteList(ctx, 1, 1); err == nil {
		t.Fatal("active audience deletion succeeded")
	}
	if value, err := db.ListSetting(ctx, 1, "goodbye_html"); err != nil || value != "Still owned" {
		t.Fatalf("failed deletion lost settings: %q %v", value, err)
	}
	var jobs int
	if err := db.Reader.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("failed deletion lost queued work: %d %v", jobs, err)
	}
}
