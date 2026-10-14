package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestChartHistoryBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Apply the preceding schema, then upgrade existing counters and receipts.
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "014_timezone_chart_history.sql" {
			break
		}
		source, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(source)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UnixMilli()
	for _, q := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'History','Sender','from@example.org',1)`,
		`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'history','History','single',1)`,
		`INSERT INTO list_counters(list_id,active) VALUES(1,12)`,
		`INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'a@example.org',1,1),(2,1,'b@example.org',1,1)`,
		`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign','sent','History','',1,1)`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,message_id,sent_at) VALUES(1,1,'sent','one',?),(1,2,'sent','two',?)`, now, now); err != nil {
		t.Fatal(err)
	}
	source, err := migrations.ReadFile("migrations/014_timezone_chart_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(source)); err != nil {
		t.Fatal(err)
	}
	var active, sent int
	if err = db.QueryRow(`SELECT active FROM list_active_history WHERE list_id=1`).Scan(&active); err != nil || active != 12 {
		t.Fatal(active, err)
	}
	if err = db.QueryRow(`SELECT sent FROM send_activity WHERE brand_id=1 AND at=?`, now/60000*60000).Scan(&sent); err != nil || sent != 2 {
		t.Fatal(sent, err)
	}
	// A repeated receipt must not add to the migrated acceptance counter.
	if _, err = db.Exec(`UPDATE campaign_recipients SET status='sent',sent_at=sent_at WHERE campaign_id=1`); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT sum(sent) FROM send_activity`).Scan(&sent); err != nil || sent != 2 {
		t.Fatal(sent, err)
	}
}
