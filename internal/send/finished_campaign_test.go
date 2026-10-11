package send

import (
	"context"
	"strings"
	"testing"
)

func TestFinishedCampaignCannotSend(t *testing.T) {
	for _, status := range []string{"sent", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			db, engine, sender := fixture(t, 1)
			if _, err := db.Writer.Exec("UPDATE campaigns SET status=?,finished_at=1 WHERE id=1", status); err != nil {
				t.Fatal(err)
			}
			if err := engine.RunCampaign(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "campaign is "+status) {
				t.Fatalf("terminal campaign run: %v", err)
			}
			if calls := sender.Calls.Load(); calls != 0 {
				t.Fatalf("terminal campaign sent %d emails", calls)
			}
			var recipients int
			if err := db.Reader.QueryRow("SELECT count(*) FROM campaign_recipients").Scan(&recipients); err != nil || recipients != 0 {
				t.Fatalf("terminal campaign prepared %d recipients: %v", recipients, err)
			}
		})
	}
}
