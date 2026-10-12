package send

import (
	"context"
	"fmt"
	"postbox/internal/events"
	"testing"
)

func TestSESReceiptBeforeSenderResultPreservesAcceptance(t *testing.T) {
	for _, receipt := range []struct{ name, body string }{
		{"delivery", `"eventType":"Delivery","delivery":{"recipients":["reader1@example.org"]}`},
		{"complaint", `"eventType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"reader1@example.org"}]}`},
		{"bounce", `"eventType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"reader1@example.org"}]}`},
	} {
		t.Run(receipt.name, func(t *testing.T) {
			db, engine, _ := fixture(t, 1)
			ingestion := events.New(db, &events.Config{BrandID: 1})
			var eventErr error
			sender := &policySender{errors: []error{context.DeadlineExceeded}}
			sender.afterSend = func() {
				request := sender.requests[len(sender.requests)-1]
				if request.CampaignIdentity == "" || request.SubscriberIdentity == "" || request.Attempt != 1 {
					eventErr = fmt.Errorf("dispatch correlation missing: %+v", request)
					return
				}
				payload := []byte(fmt.Sprintf(`{%s,"mail":{"messageId":"accepted-before-response","tags":{"brand_id":["1"],"campaign_id":["1"],"campaign_identity":[%q],"subscriber_identity":[%q],"send_attempt":["%d"]}}}`, receipt.body, request.CampaignIdentity, request.SubscriberIdentity, request.Attempt))
				eventErr = ingestion.Process(context.Background(), "receipt-during-send", payload)
			}
			engine.sender = sender
			if err := engine.RunCampaign(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if eventErr != nil {
				t.Fatal(eventErr)
			}
			var status, message string
			if err := db.Reader.QueryRow(`SELECT status,message_id FROM campaign_recipients WHERE campaign_id=1`).Scan(&status, &message); err != nil || status != "sent" || message != "accepted-before-response" {
				t.Fatalf("recipient=%s message=%s err=%v", status, message, err)
			}
			var sent, unknown int
			if err := db.Reader.QueryRow(`SELECT sent,unknown FROM campaign_stats WHERE campaign_id=1`).Scan(&sent, &unknown); err != nil || sent != 1 || unknown != 0 {
				t.Fatalf("sent=%d unknown=%d err=%v", sent, unknown, err)
			}
			assertPolicyJournal(t, db, []string{"sent"})
			if err := db.Reader.QueryRow(`SELECT outcome FROM campaign_dispatch_ledger WHERE campaign_id=1`).Scan(&status); err != nil || status != "sent" {
				t.Fatalf("ledger=%s err=%v", status, err)
			}
			if sender.calls != 1 {
				t.Fatalf("accepted receipt retried %d times", sender.calls)
			}
		})
	}
}
