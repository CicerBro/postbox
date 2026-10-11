package consent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path/filepath"
	"postbox/internal/store"
	"testing"
)

func TestUnsubscribeMembershipsMaintainsCountersAndInvalidatesTokens(t *testing.T) {
	for _, reason := range []string{"unsubscribe", "bounce", "complaint"} {
		t.Run(reason, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, q := range []string{
				"INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Test','Sender','sender@example.org',1)",
				"INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'one','One','single',1),(2,1,'two','Two','double',1),(3,1,'three','Three','single',1)",
				"INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'reader@example.org',1,1)",
				"INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at,confirmation_started_at) VALUES(1,1,'subscribed',1,1,NULL),(2,1,'unconfirmed',1,1,1),(3,1,'unsubscribed',1,1,NULL)",
				"INSERT INTO list_counters(list_id,active,subscribed) VALUES(1,1,1)",
			} {
				if _, err = db.Writer.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			hash := sha256.Sum256([]byte("pending"))
			if _, err = db.Writer.Exec("INSERT INTO subscription_confirmations(token_hash,list_id,subscriber_id,expires_at) VALUES(?,2,1,9999999999999)", hash[:]); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			transition := func() ([]int64, error) {
				var ids []int64
				err := db.Immediate(ctx, func(c *sql.Conn) error {
					var e error
					ids, e = UnsubscribeMembershipsTx(ctx, c, 1, 0, 86400000, reason)
					return e
				})
				return ids, err
			}
			ids, err := transition()
			if err != nil || len(ids) != 2 {
				t.Fatalf("changed memberships=%v err=%v", ids, err)
			}
			ids, err = transition()
			if err != nil || len(ids) != 0 {
				t.Fatalf("repeated transition=%v err=%v", ids, err)
			}
			var active, unsubscribed, bounced, complained int
			if err = db.Reader.QueryRow("SELECT active,unsubscribed,bounced,complained FROM list_counters WHERE list_id=1").Scan(&active, &unsubscribed, &bounced, &complained); err != nil {
				t.Fatal(err)
			}
			wantBounce, wantComplaint := 0, 0
			if reason == "bounce" {
				wantBounce = 1
			}
			if reason == "complaint" {
				wantComplaint = 1
			}
			if active != 0 || unsubscribed != 1 || bounced != wantBounce || complained != wantComplaint {
				t.Fatalf("counters=%d %d %d %d", active, unsubscribed, bounced, complained)
			}
			if err = db.Reader.QueryRow("SELECT active,unsubscribed,bounced,complained FROM list_daily_stats WHERE list_id=1 AND day=86400000").Scan(&active, &unsubscribed, &bounced, &complained); err != nil || active != 0 || unsubscribed != 1 || bounced != wantBounce || complained != wantComplaint {
				t.Fatalf("daily=%d %d %d %d err=%v", active, unsubscribed, bounced, complained, err)
			}
			var remaining int
			if err = db.Reader.QueryRow("SELECT count(*) FROM list_subscribers WHERE status!='unsubscribed' OR confirmation_started_at IS NOT NULL").Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("memberships remain active/pending=%d err=%v", remaining, err)
			}
			if err = db.Reader.QueryRow("SELECT count(*) FROM subscription_confirmations").Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("confirmation tokens remain=%d err=%v", remaining, err)
			}
			if err = db.Reader.QueryRow("SELECT count(*) FROM list_counters WHERE list_id=2").Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("pending membership incremented unsubscribe counters=%d err=%v", remaining, err)
			}
		})
	}
}
