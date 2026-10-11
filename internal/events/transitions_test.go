package events

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBrandEventsIsolateSuppressionsAndBounceCounts(t *testing.T) {
	for _, typ := range []string{"Complaint", "Permanent", "Transient"} {
		t.Run(typ, func(t *testing.T) {
			db := eventDB(t)
			s := New(db, &Config{BrandID: 1, SoftBounceLimit: 1})
			payload := eventPayload(typ, "ses-id")
			if err := s.Process(context.Background(), "scoped", payload); err != nil {
				t.Fatal(err)
			}
			var count int
			for _, q := range []string{
				`SELECT count(*) FROM suppressions WHERE brand_id IS NULL OR brand_id=2`,
				`SELECT count(*) FROM subscribers WHERE id=2 AND (status!='active' OR soft_bounce_count!=0)`,
				`SELECT count(*) FROM list_subscribers WHERE list_id=2 AND status!='subscribed'`,
				`SELECT count(*) FROM campaign_recipients WHERE campaign_id=2 AND status!='pending'`,
			} {
				if err := db.Reader.QueryRow(q).Scan(&count); err != nil || count != 0 {
					t.Fatalf("cross-brand change: %s: count=%d err=%v", q, count, err)
				}
			}
			if err := db.Reader.QueryRow(`SELECT count(*) FROM suppressions WHERE brand_id=1`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("brand suppression count=%d err=%v", count, err)
			}
		})
	}
}

func TestUnscopableSESReceiptRejectsTransaction(t *testing.T) {
	db := eventDB(t)
	s := New(db, &Config{})
	payload := []byte(`{"eventType":"Complaint","mail":{"messageId":"unknown"},"complaint":{"complainedRecipients":[{"emailAddress":"user@example.com"}]}}`)
	if err := s.Process(context.Background(), "unscopable", payload); err == nil {
		t.Fatal("unscopable event accepted")
	}
	var count int
	if err := db.Reader.QueryRow(`SELECT (SELECT count(*) FROM suppressions)+(SELECT count(*) FROM ses_events)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected event changed state: count=%d err=%v", count, err)
	}
}

func TestRepeatedRecipientInReceiptCountsOneBounce(t *testing.T) {
	db := eventDB(t)
	s := New(db, &Config{BrandID: 1})
	payload := []byte(`{"eventType":"Bounce","mail":{"messageId":"ses-id","tags":{"brand_id":["1"]}},"bounce":{"bounceType":"Transient","bouncedRecipients":[{"emailAddress":"user@example.com"},{"emailAddress":"USER@example.com"}]}}`)
	if err := s.Process(context.Background(), "duplicate-address", payload); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Reader.QueryRow(`SELECT soft_bounce_count FROM subscribers WHERE id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bounce count=%d err=%v", count, err)
	}
}

func TestSESReceiptsReconcileAcceptance(t *testing.T) {
	for _, typ := range []string{"Delivery", "Complaint", "Permanent"} {
		for _, state := range []string{"claimed", "unknown"} {
			t.Run(typ+"/"+state, func(t *testing.T) {
				db := eventDB(t)
				for _, q := range []string{
					fmt.Sprintf(`UPDATE campaign_recipients SET status='%s',message_id=NULL,error='lost response' WHERE campaign_id=1`, state),
					fmt.Sprintf(`UPDATE campaign_stats SET unknown=%d WHERE campaign_id=1`, boolInt(state == "unknown")),
					fmt.Sprintf(`INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome,dispatch_generation,error) VALUES(1,1,1,3600000,'%s',1,'lost response')`, map[string]string{"claimed": "started", "unknown": "unknown"}[state]),
					fmt.Sprintf(`INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at) VALUES(1,zeroblob(32),'%s',1,1,1,1)`, map[string]string{"claimed": "started", "unknown": "unknown"}[state]),
				} {
					if _, err := db.Writer.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
				s := New(db, &Config{BrandID: 1})
				for _, id := range []string{"receipt", "receipt", "redelivered"} {
					if err := s.Process(context.Background(), id, eventPayload(typ, "accepted-id")); err != nil {
						t.Fatal(err)
					}
				}
				var status, message string
				var at int64
				if err := db.Reader.QueryRow(`SELECT status,message_id,sent_at FROM campaign_recipients WHERE campaign_id=1`).Scan(&status, &message, &at); err != nil || status != "sent" || message != "accepted-id" || at != 3600000 {
					t.Fatalf("recipient=%s %s %d err=%v", status, message, at, err)
				}
				var sent, unknown int
				if err := db.Reader.QueryRow(`SELECT sent,unknown FROM campaign_stats WHERE campaign_id=1`).Scan(&sent, &unknown); err != nil || sent != 1 || unknown != 0 {
					t.Fatalf("sent=%d unknown=%d err=%v", sent, unknown, err)
				}
				if err := db.Reader.QueryRow(`SELECT outcome,message_id FROM send_attempts WHERE campaign_id=1`).Scan(&status, &message); err != nil || status != "sent" || message != "accepted-id" {
					t.Fatalf("attempt=%s %s err=%v", status, message, err)
				}
				if err := db.Reader.QueryRow(`SELECT outcome FROM campaign_dispatch_ledger WHERE campaign_id=1`).Scan(&status); err != nil || status != "sent" {
					t.Fatalf("ledger=%s err=%v", status, err)
				}
				if err := db.Reader.QueryRow(`SELECT sent FROM send_hourly WHERE brand_id=1 AND hour=3600000`).Scan(&sent); err != nil || sent != 1 {
					t.Fatalf("hourly sent=%d err=%v", sent, err)
				}
			})
		}
	}
}

func TestSnapshotRecipientMatchingUsesCompositeIndex(t *testing.T) {
	db := eventDB(t)
	rows, err := db.Reader.Query(`EXPLAIN QUERY PLAN `+snapshotMatchSQL, "1", "user@example.com", "1", "early-id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "campaign_recipients_snapshot_email (campaign_id=? AND snapshot_email=?)") {
		t.Fatalf("fallback scans campaign recipients: %v", plan)
	}
}

func eventPayload(typ, messageID string) []byte {
	mail := fmt.Sprintf(`"mail":{"messageId":%q,"tags":{"brand_id":["1"],"campaign_id":["1"]}}`, messageID)
	switch typ {
	case "Delivery":
		return []byte(`{"eventType":"Delivery",` + mail + `,"delivery":{"recipients":["user@example.com"]}}`)
	case "Complaint":
		return []byte(`{"eventType":"Complaint",` + mail + `,"complaint":{"complainedRecipients":[{"emailAddress":"user@example.com"}]}}`)
	default:
		return []byte(`{"eventType":"Bounce",` + mail + fmt.Sprintf(`,"bounce":{"bounceType":%q,"bouncedRecipients":[{"emailAddress":"user@example.com"}]}}`, typ))
	}
}
