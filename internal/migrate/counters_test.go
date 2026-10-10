package migrate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"postbox/internal/jobs"
	"postbox/internal/store"
)

type counters struct{ active, subscribed, unsubscribed, bounced, complained int64 }

func readCounters(t *testing.T, db *store.DB, table string, list int64, day int64) counters {
	t.Helper()
	query := "SELECT active,subscribed,unsubscribed,bounced,complained FROM " + table + " WHERE list_id=?"
	args := []any{list}
	if table == "list_daily_stats" {
		query += " AND day=?"
		args = append(args, day)
	}
	var c counters
	if err := db.Reader.QueryRow(query, args...).Scan(&c.active, &c.subscribed, &c.unsubscribed, &c.bounced, &c.complained); err != nil {
		t.Fatal(err)
	}
	return c
}

func importCounters(t *testing.T, db *store.DB, dump string, batch int) {
	t.Helper()
	if _, err := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "counters", BatchSize: batch}); err != nil {
		t.Fatal(err)
	}
}

const counterLists = `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');
INSERT INTO lists(id,app,name) VALUES(1,1,'Main'),(2,1,'Other'),(3,1,'Empty');`

func TestImportedCounterBaselineAndRepeat(t *testing.T) {
	db := testDB(t)
	dump := counterLists + `INSERT INTO subscribers(id,list,email,status,join_date) VALUES
 (1,1,'active@example.com','active','2020-01-01'),
 (2,1,'pending@example.com','unconfirmed','2020-01-01'),
 (3,1,'off@example.com','unsubscribed','2020-01-01'),
 (4,1,'bounce@example.com','bounced','2020-01-01'),
 (5,1,'complaint@example.com','complained','2020-01-01'),
 (6,2,'ACTIVE@example.com','active','2020-01-01'),
 (7,1,'active@example.com','active','2020-01-01');`
	importCounters(t, db, dump, 1)
	at := time.Now().UTC()
	day := at.UnixMilli() / 86400000 * 86400000
	runner := jobs.New(db, jobs.Config{})
	for list := int64(1); list <= 3; list++ {
		want := counters{}
		if list != 3 {
			want = counters{active: 1, subscribed: 1}
		}
		if got := readCounters(t, db, "list_counters", list, 0); got != want {
			t.Fatalf("list %d counter = %+v, want %+v", list, got, want)
		}
		if got := readCounters(t, db, "list_daily_stats", list, day); got != (counters{active: want.active}) {
			t.Fatalf("list %d initial daily = %+v", list, got)
		}
	}
	if err := runner.Rollup(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	importCounters(t, db, dump, 2)
	if err := runner.Rollup(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	for list := int64(1); list <= 3; list++ {
		want := counters{}
		if list != 3 {
			want = counters{active: 1, subscribed: 1}
		}
		if got := readCounters(t, db, "list_counters", list, 0); got != want {
			t.Fatalf("repeat list %d counter = %+v", list, got)
		}
		if got := readCounters(t, db, "list_daily_stats", list, day); got != (counters{active: want.active}) {
			t.Fatalf("repeat list %d daily = %+v", list, got)
		}
	}
	if err := runner.Rollup(context.Background(), at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := readCounters(t, db, "list_daily_stats", 1, day+86400000); got != (counters{active: 1}) {
		t.Fatalf("import became tomorrow's growth: %+v", got)
	}
}

func TestImportedStatusTransitionsUpdateAllMembershipCounters(t *testing.T) {
	for _, status := range []string{"unconfirmed", "unsubscribed", "bounced", "complained"} {
		t.Run(status, func(t *testing.T) {
			db := testDB(t)
			initial := counterLists + `INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'member@example.com','active'),(2,2,'member@example.com','active');`
			importCounters(t, db, initial, 1)
			// The changed scan omits list 2's subscriber row entirely. Global restrictions
			// must still deactivate its durable membership from the earlier import.
			changed := counterLists + fmt.Sprintf(`INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'member@example.com','%s');`, status)
			importCounters(t, db, changed, 1)
			importCounters(t, db, changed, 1)
			want := counters{subscribed: 1, unsubscribed: 1}
			if status == "bounced" {
				want.bounced = 1
			}
			if status == "complained" {
				want.complained = 1
			}
			if got := readCounters(t, db, "list_counters", 1, 0); got != want {
				t.Fatalf("changed main = %+v, want %+v", got, want)
			}
			other := counters{active: 1, subscribed: 1}
			if status == "bounced" || status == "complained" {
				other = want
			}
			if got := readCounters(t, db, "list_counters", 2, 0); got != other {
				t.Fatalf("changed other = %+v, want %+v", got, other)
			}
			at := time.Now().UTC()
			if err := jobs.New(db, jobs.Config{}).Rollup(context.Background(), at); err != nil {
				t.Fatal(err)
			}
			day := at.UnixMilli() / 86400000 * 86400000
			if got := readCounters(t, db, "list_daily_stats", 1, day); got != (counters{}) {
				t.Fatalf("historic status change counted today: %+v", got)
			}
		})
	}
}

func TestImportedBaselinePreservesLiveGrowth(t *testing.T) {
	db := testDB(t)
	importCounters(t, db, counterLists, 1)
	ctx := context.Background()
	at := time.Now().UTC()
	day := at.UnixMilli() / 86400000 * 86400000
	// Existing snapshots and counters model two previous subscriptions and one
	// genuine subscription today. Today's rollup has already run before import.
	_, err := db.Writer.Exec(`UPDATE list_counters SET active=3,subscribed=3 WHERE list_id=1;
 INSERT INTO list_counter_snapshots(list_id,day,subscribed) VALUES(1,?,2),(1,?,3);
 UPDATE list_daily_stats SET active=3,subscribed=1 WHERE list_id=1 AND day=?`, day-86400000, day, day)
	if err != nil {
		t.Fatal(err)
	}
	importCounters(t, db, counterLists+`INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'historic@example.com','active');`, 1)
	runner := jobs.New(db, jobs.Config{})
	if err = runner.Rollup(ctx, at); err != nil {
		t.Fatal(err)
	}
	if got := readCounters(t, db, "list_daily_stats", 1, day); got != (counters{active: 4, subscribed: 1}) {
		t.Fatalf("real growth changed: %+v", got)
	}
	if err = runner.Rollup(ctx, at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := readCounters(t, db, "list_daily_stats", 1, day+86400000); got != (counters{active: 4}) {
		t.Fatalf("import counted tomorrow: %+v", got)
	}
}

func TestGlobalBounceUpdatesCountersAcrossBrands(t *testing.T) {
	db := testDB(t)
	initial := counterLists + `INSERT INTO apps(id,app_name,from_email) VALUES(2,'Other','other@example.com');
INSERT INTO lists(id,app,name) VALUES(4,2,'Other brand');
INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'shared@example.com','active'),(2,4,'shared@example.com','active');`
	importCounters(t, db, initial, 1)
	// Only one brand appears in the changed subscriber input.
	changed := counterLists + `INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'shared@example.com','bounced');`
	importCounters(t, db, changed, 1)
	for _, list := range []int64{1, 4} {
		want := counters{subscribed: 1, unsubscribed: 1, bounced: 1}
		if got := readCounters(t, db, "list_counters", list, 0); got != want {
			t.Fatalf("brand list %d = %+v, want %+v", list, got, want)
		}
	}
	// A stale active source row in the other brand cannot revive the globally
	// suppressed membership or count another subscription.
	importCounters(t, db, initial, 2)
	if got := readCounters(t, db, "list_counters", 4, 0); got != (counters{subscribed: 1, unsubscribed: 1, bounced: 1}) {
		t.Fatalf("stale source revived other brand: %+v", got)
	}
}

func TestCounterMaintenanceRollsBackWithFailedBatch(t *testing.T) {
	db := testDB(t)
	dump := counterLists + `INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'valid@example.com','active'),(2,1,'invalid','active');`
	_, err := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "counters", BatchSize: 2})
	if err == nil {
		t.Fatal("invalid row accepted")
	}
	if got := readCounters(t, db, "list_counters", 1, 0); got != (counters{}) {
		t.Fatalf("failed batch leaked counters: %+v", got)
	}
	if scalar(t, db, "SELECT count(*) FROM list_counter_snapshots") != 0 {
		t.Fatal("failed batch leaked baseline")
	}
	if scalar(t, db, "SELECT count(*) FROM subscribers") != 0 {
		t.Fatal("failed batch leaked contact")
	}
	importCounters(t, db, counterLists+`INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'valid@example.com','active');`, 1)
	if got := readCounters(t, db, "list_counters", 1, 0); got != (counters{active: 1, subscribed: 1}) {
		t.Fatalf("resume counter: %+v", got)
	}
}

func TestGlobalMembershipCounterLookupUsesIndexes(t *testing.T) {
	db := testDB(t)
	rows, err := db.Reader.Query(`EXPLAIN QUERY PLAN SELECT ls.list_id,ls.subscriber_id,ls.status
 FROM subscribers s JOIN list_subscribers ls ON ls.subscriber_id=s.id
 WHERE s.email=? AND ls.status!='unsubscribed'`, "member@example.com")
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
		if strings.Contains(detail, "SCAN ") {
			t.Fatalf("unbounded scan: %s", detail)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "subscribers_email") || !strings.Contains(text, "list_subscribers_subscriber") {
		t.Fatalf("unexpected plan: %s", text)
	}
}
