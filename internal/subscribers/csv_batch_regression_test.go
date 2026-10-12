package subscribers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"postbox/internal/store"
	"strings"
	"sync/atomic"
	"testing"

	"modernc.org/sqlite"
)

type importCountingDriver struct {
	sqlite.Driver
	listReads, membershipReads atomic.Int64
	transactions               atomic.Int64
	parameterLimit             int
}

type importCountingConn struct {
	driver.Conn
	counts *importCountingDriver
}

func (d *importCountingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &importCountingConn{Conn: c, counts: d}, nil
}

func (c *importCountingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if c.counts.parameterLimit > 0 && len(args) > c.counts.parameterLimit {
		return nil, fmt.Errorf("query exceeds %d parameters: %d", c.counts.parameterLimit, len(args))
	}
	if strings.Contains(q, "FROM lists") {
		c.counts.listReads.Add(1)
	}
	if strings.Contains(q, "FROM list_subscribers") || strings.Contains(q, "JOIN list_subscribers") {
		c.counts.membershipReads.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c *importCountingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if q == "BEGIN IMMEDIATE" {
		c.counts.transactions.Add(1)
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

var importDriverSequence atomic.Int64

func TestImportCSVBoundsMembershipReadsPerBatch(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	l.OptIn = "single"
	if err := s.UpdateList(ctx, l); err != nil {
		t.Fatal(err)
	}
	existing, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "existing@example.org", ListIDs: []int64{l.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, existing.ID, l.ID); err != nil {
		t.Fatal(err)
	}
	suppressed, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "bounced@example.org", Status: "bounced"})
	if err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err = s.db.Reader.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.db.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	counts := &importCountingDriver{}
	driverName := fmt.Sprintf("import-read-count-%d", importDriverSequence.Add(1))
	sql.Register(driverName, counts)
	s.db.Writer, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Writer.SetMaxOpenConns(1)
	var input strings.Builder
	input.WriteString("email,name,status\n")
	// Duplicate addresses exercise cache writes as well as existing membership reads.
	input.WriteString("existing@example.org,Existing,active\nbounced@example.org,Bounced,active\n")
	for i := 0; i < 1001; i++ {
		fmt.Fprintf(&input, "reader%d@example.org,First,active\nreader%d@example.org,Second,active\n", i, i)
	}
	const batchSize = 700
	result, err := s.ImportCSV(ctx, strings.NewReader(input.String()), ImportOptions{BrandID: b.ID, ListIDs: []int64{l.ID}, BatchSize: batchSize})
	if err != nil || result.Imported != 2004 {
		t.Fatalf("import=%+v err=%v", result, err)
	}
	// Three batches load list policy once each. Membership reads use chunks of 500:
	// two queries for each 700-row batch and two for the final 604-row batch.
	// One additional membership read applies the suppressed contact transition.
	if got := counts.listReads.Load(); got != 3 {
		t.Fatalf("list configuration SELECTs=%d want=3", got)
	}
	if got := counts.membershipReads.Load(); got != 7 {
		t.Fatalf("membership SELECTs=%d want=7", got)
	}
	var active, subscribed, unsubscribed int
	if err = s.db.Reader.QueryRow("SELECT active,subscribed,unsubscribed FROM list_counters WHERE list_id=?", l.ID).Scan(&active, &subscribed, &unsubscribed); err != nil {
		t.Fatal(err)
	}
	if active != 1001 || subscribed != 1002 || unsubscribed != 1 {
		t.Fatalf("duplicate import inflated counters: %d %d %d", active, subscribed, unsubscribed)
	}
	for _, v := range []Subscriber{existing, suppressed} {
		var status string
		if err = s.db.Reader.QueryRow("SELECT status FROM list_subscribers WHERE subscriber_id=? AND list_id=?", v.ID, l.ID).Scan(&status); err != nil || status != "unsubscribed" {
			t.Fatalf("revived %s: %s %v", v.Email, status, err)
		}
	}
}

func BenchmarkImportCSVWithLists(b *testing.B) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing"
		}
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			var input strings.Builder
			input.WriteString("email,name\n")
			for i := 0; i < 10000; i++ {
				fmt.Fprintf(&input, "reader%d@example.org,Reader %d\n", i, i)
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				db, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
				if err != nil {
					b.Fatal(err)
				}
				s := New(db)
				brand, err := s.CreateBrand(ctx, Brand{Name: "Benchmark", FromEmail: "sender@example.org"})
				if err != nil {
					b.Fatal(err)
				}
				list, err := s.CreateList(ctx, List{BrandID: brand.ID, Name: "Readers", OptIn: "single"})
				if err != nil {
					b.Fatal(err)
				}
				opt := ImportOptions{BrandID: brand.ID, ListIDs: []int64{list.ID}}
				if existing {
					if _, err = s.ImportCSV(ctx, strings.NewReader(input.String()), opt); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				result, err := s.ImportCSV(ctx, strings.NewReader(input.String()), opt)
				if err != nil || result.Imported != 10000 {
					b.Fatalf("import=%+v err=%v", result, err)
				}
				b.StopTimer()
				db.Close()
			}
		})
	}
}
