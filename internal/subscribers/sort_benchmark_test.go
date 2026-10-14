package subscribers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"postbox/internal/store"
)

// Run explicitly: POSTBOX_SORT_BENCHMARK=1 go test ./internal/subscribers -run '^TestSubscriberSortBenchmark$' -v -count=1
// Temporary synthetic data only. This measures SQLite query/index maintenance,
// not HTTP, CSV parsing, activity records or subscription transitions.
func TestSubscriberSortBenchmark(t *testing.T) {
	if os.Getenv("POSTBOX_SORT_BENCHMARK") != "1" {
		t.Skip("opt-in 350k synthetic benchmark")
	}
	type result struct {
		Indexed         bool               `json:"indexed"`
		Synchronous     int                `json:"sqlite_synchronous"`
		FullFSync       int                `json:"sqlite_fullfsync"`
		SeedMS          float64            `json:"seed_ms"`
		DatabaseBytes   int64              `json:"database_bytes"`
		BulkInsert10kMS float64            `json:"bulk_insert_10k_ms"`
		BulkUpdate10kMS float64            `json:"bulk_update_10k_ms"`
		QueryMedianMS   map[string]float64 `json:"query_median_ms"`
	}
	var results []result
	for _, indexed := range []bool{false, true} {
		if os.Getenv("POSTBOX_SORT_BENCHMARK_VARIANT") == "indexed" && !indexed {
			continue
		}
		path := filepath.Join(t.TempDir(), "sort.db")
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var synchronous, fullFSync int
		if err = db.Writer.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if err = db.Writer.QueryRow("PRAGMA fullfsync").Scan(&fullFSync); err != nil {
			t.Fatal(err)
		}
		s := New(db)
		brand, err := s.CreateBrand(context.Background(), Brand{Name: "Synthetic", FromEmail: "sender@example.org"})
		if err != nil {
			t.Fatal(err)
		}
		list, err := s.CreateList(context.Background(), List{BrandID: brand.ID, Name: "Synthetic", OptIn: "single"})
		if err != nil {
			t.Fatal(err)
		}
		if !indexed {
			if _, err = db.Writer.Exec("DROP INDEX subscribers_brand_name_id; DROP INDEX subscribers_brand_consent_id"); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		if _, err = db.Writer.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<350000)
INSERT INTO subscribers(brand_id,email,name,status,gdpr_consent_at,created_at,updated_at)
SELECT ?,printf('person%06d@example.org',i),printf('Name%04d',(i*7919)%10000),
CASE WHEN i%10=0 THEN 'bounced' ELSE 'active' END,CASE WHEN i%3=0 THEN NULL ELSE (i*3571)%10000 END,i%10000,0 FROM n`, brand.ID); err != nil {
			t.Fatal(err)
		}
		r := result{Indexed: indexed, Synchronous: synchronous, FullFSync: fullFSync, SeedMS: float64(time.Since(start).Microseconds()) / 1000, QueryMedianMS: map[string]float64{}}
		t.Logf("indexed=%t: seeded 350000 rows in %.3fms", indexed, r.SeedMS)
		if _, err = db.Writer.Exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) SELECT ?,id,'subscribed',0,0 FROM subscribers WHERE id%10=0", list.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Writer.Exec("ANALYZE; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		r.DatabaseBytes = info.Size()
		for _, column := range []string{"name", "gdpr_consent_at"} {
			if only := os.Getenv("POSTBOX_SORT_BENCHMARK_SORT"); only != "" && column != only {
				continue
			}
			for _, direction := range []string{"asc", "desc"} {
				for _, scope := range []string{"brand", "status", "prefix", "list"} {
					f := SearchFilter{Sort: column, Direction: direction}
					switch scope {
					case "status":
						f.Status = "bounced"
					case "prefix":
						f.Email = "person349"
					case "list":
						f.ListID = list.ID
						f.MembershipStatus = "subscribed"
					}
					for _, after := range []int64{0, 175000} {
						q, args, err := subscriberSearchQuery(brand.ID, after, 100, f)
						if err != nil {
							t.Fatal(err)
						}
						var times []float64
						for i := 0; i < 9; i++ {
							start = time.Now()
							rows, err := db.Reader.Query(q, args...)
							if err != nil {
								t.Fatal(err)
							}
							for rows.Next() {
								if _, err = scanSubscriber(rows); err != nil {
									t.Fatal(err)
								}
							}
							if err = rows.Err(); err != nil {
								t.Fatal(err)
							}
							rows.Close()
							if i > 1 {
								times = append(times, float64(time.Since(start).Microseconds())/1000)
							}
						}
						sort.Float64s(times)
						page := "first"
						if after > 0 {
							page = "cursor"
						}
						r.QueryMedianMS[column+"/"+direction+"/"+scope+"/"+page] = times[len(times)/2]
					}
				}
			}
		}
		start = time.Now()
		if _, err = db.Writer.Exec(`WITH RECURSIVE n(i) AS (VALUES(350001) UNION ALL SELECT i+1 FROM n WHERE i<360000)
INSERT INTO subscribers(brand_id,email,name,status,gdpr_consent_at,created_at,updated_at)
SELECT ?,printf('person%06d@example.org',i),printf('Name%04d',(i*7919)%10000),'active',(i*3571)%10000,i%10000,0 FROM n`, brand.ID); err != nil {
			t.Fatal(err)
		}
		r.BulkInsert10kMS = float64(time.Since(start).Microseconds()) / 1000
		start = time.Now()
		if _, err = db.Writer.Exec("UPDATE subscribers SET name='Changed'||id,gdpr_consent_at=12345 WHERE id<=10000"); err != nil {
			t.Fatal(err)
		}
		r.BulkUpdate10kMS = float64(time.Since(start).Microseconds()) / 1000
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		results = append(results, r)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
}
