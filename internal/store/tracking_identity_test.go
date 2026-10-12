package store

import (
	"database/sql"
	"testing"
)

func TestTrackingIdentityMigrationPreservesLegacyBoundary(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE subscribers(id INTEGER PRIMARY KEY,email TEXT);CREATE TABLE campaigns(id INTEGER PRIMARY KEY,uuid TEXT);INSERT INTO subscribers VALUES(1,'original@example.org');INSERT INTO campaigns VALUES(1,'original');`); err != nil {
		t.Fatal(err)
	}
	source, err := migrations.ReadFile("migrations/011_tracking_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(source)); err != nil {
		t.Fatal(err)
	}
	source, err = migrations.ReadFile("migrations/012_campaign_tracking_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(source)); err != nil {
		t.Fatal(err)
	}
	var identity, campaignIdentity string
	var subscriberLegacy, campaignLegacy bool
	if err = db.QueryRow(`SELECT s.tracking_identity,c.tracking_identity,s.tracking_legacy,c.tracking_legacy FROM subscribers s,campaigns c WHERE s.id=1 AND c.id=1`).Scan(&identity, &campaignIdentity, &subscriberLegacy, &campaignLegacy); err != nil {
		t.Fatal(err)
	}
	if len(identity) != 32 || len(campaignIdentity) != 32 || !subscriberLegacy || !campaignLegacy {
		t.Fatalf("existing capabilities lost during migration: %q %v %v", identity, subscriberLegacy, campaignLegacy)
	}
	for _, query := range []string{`UPDATE subscribers SET tracking_identity='00000000000000000000000000000000' WHERE id=1`, `UPDATE subscribers SET tracking_legacy=0 WHERE id=1`, `UPDATE campaigns SET tracking_identity='00000000000000000000000000000000' WHERE id=1`, `UPDATE campaigns SET uuid='changed' WHERE id=1`, `UPDATE campaigns SET tracking_legacy=0 WHERE id=1`} {
		if _, err = db.Exec(query); err == nil {
			t.Fatalf("identity mutation allowed: %s", query)
		}
	}
	if _, err = db.Exec(`DELETE FROM subscribers;DELETE FROM campaigns;INSERT INTO subscribers(email) VALUES('replacement@example.org');INSERT INTO campaigns(uuid) VALUES('replacement');`); err != nil {
		t.Fatal(err)
	}
	var replacement, replacementCampaign string
	if err = db.QueryRow(`SELECT s.tracking_identity,c.tracking_identity,s.tracking_legacy,c.tracking_legacy FROM subscribers s,campaigns c WHERE s.id=1 AND c.id=1`).Scan(&replacement, &replacementCampaign, &subscriberLegacy, &campaignLegacy); err != nil {
		t.Fatal(err)
	}
	if len(replacement) != 32 || replacement == identity || len(replacementCampaign) != 32 || replacementCampaign == campaignIdentity || subscriberLegacy || campaignLegacy {
		t.Fatalf("reused numeric IDs inherited identity: %q %v %v", replacement, subscriberLegacy, campaignLegacy)
	}
}
