package subscribers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func csvImportData(t *testing.T, records ...[]string) string {
	t.Helper()
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(records); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestImportCSVDoesNotQueueEmailsOrWebhooks(t *testing.T) {
	for _, optIn := range []string{"single", "double"} {
		t.Run(optIn, func(t *testing.T) {
			ctx := context.Background()
			s, brand, list := fixture(t)
			s.SetBaseURL("https://mail.example.org")
			list.OptIn = optIn
			if err := s.UpdateList(ctx, list); err != nil {
				t.Fatal(err)
			}
			other, err := s.CreateList(ctx, List{BrandID: brand.ID, Name: "Other", OptIn: "single"})
			if err != nil {
				t.Fatal(err)
			}
			for _, email := range []string{"unsubscribe@example.org", "bounce@example.org"} {
				if _, err := s.Upsert(ctx, UpsertInput{BrandID: brand.ID, Email: email, ListIDs: []int64{list.ID, other.ID}, SubscriptionStatus: "subscribed", SuppressNotifications: true}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Writer.Exec(`INSERT INTO webhooks(brand_id,url,events,secret) VALUES(?,'https://example.org/webhook','["subscribe","unsubscribe"]','secret')`, brand.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.db.SetSetting(ctx, fmt.Sprintf("brand.%d.thankyou_email", brand.ID), `{"subject":"Welcome","html":"<p>Hello</p>"}`); err != nil {
				t.Fatal(err)
			}
			input := "email,status\nnew@example.org,active\nunsubscribe@example.org,unsubscribed\nbounce@example.org,bounced\n"
			result, err := s.ImportCSV(ctx, strings.NewReader(input), ImportOptions{BrandID: brand.ID, ListIDs: []int64{list.ID}, BatchSize: 1})
			if err != nil || result.Imported != 3 {
				t.Fatalf("import: %+v, %v", result, err)
			}
			var jobs int
			if err := s.db.Reader.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
				t.Fatalf("import queued %d outbound jobs: %v", jobs, err)
			}
			var status string
			want := "subscribed"
			if optIn == "double" {
				want = "unconfirmed"
			}
			if err := s.db.Reader.QueryRow(`SELECT ls.status FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE s.email='new@example.org' AND ls.list_id=?`, list.ID).Scan(&status); err != nil || status != want {
				t.Fatalf("new membership: %q, want %q: %v", status, want, err)
			}
			if err := s.db.Reader.QueryRow(`SELECT ls.status FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE s.email='bounce@example.org' AND ls.list_id=?`, other.ID).Scan(&status); err != nil || status != "unsubscribed" {
				t.Fatalf("bounce failed to suppress other membership: %q: %v", status, err)
			}
		})
	}
}

func TestImportCSVFieldsObjectsAndPrecedence(t *testing.T) {
	ctx := context.Background()
	for _, reverse := range []bool{false, true} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse=%t/preview=%t", reverse, dryRun), func(t *testing.T) {
				s, b, _ := fixture(t)
				header := []string{"email", "fields", "Age"}
				if reverse {
					header = []string{"email", "Age", "fields"}
				}
				records := [][]string{header}
				for i, value := range []string{"null", `{"age":99,"country":"NL"}`, "[]", `"scalar"`, "42", "true", "{"} {
					row := []string{fmt.Sprintf("reader%d@example.org", i), value, "32"}
					if reverse {
						row[1], row[2] = row[2], row[1]
					}
					records = append(records, row)
				}
				if _, err := s.SaveCustomField(ctx, CustomField{BrandID: b.ID, Key: "age", Type: "number"}); err != nil {
					t.Fatal(err)
				}
				result, err := s.ImportCSV(ctx, strings.NewReader(csvImportData(t, records...)), ImportOptions{BrandID: b.ID, DryRun: dryRun, BatchSize: 1, Mapping: map[string]string{"email": "email", "fields": "fields", "Age": "fields.age"}})
				if err != nil || result.Rows != 7 || result.Invalid != 5 || len(result.Preview) != 2 {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				wantImported := int64(2)
				if dryRun {
					wantImported = 0
				}
				if result.Imported != wantImported {
					t.Fatalf("imported=%d want=%d", result.Imported, wantImported)
				}
				for i, in := range result.Preview {
					want := map[string]any{"age": float64(32)}
					if i == 1 {
						want["country"] = "NL"
					}
					if !reflect.DeepEqual(in.Fields, want) {
						t.Fatalf("preview fields=%v want=%v", in.Fields, want)
					}
					if !dryRun {
						stored, err := s.GetByEmail(ctx, b.ID, in.Email)
						if err != nil || !reflect.DeepEqual(stored.Fields, want) {
							t.Fatalf("stored=%+v error=%v", stored, err)
						}
					}
				}
				if dryRun {
					subs, err := s.Search(ctx, b.ID, 0, 100)
					if err != nil || len(subs) != 0 {
						t.Fatalf("preview wrote subscribers: %v %v", subs, err)
					}
				}
			})
		}
	}
}

func TestImportCSVRejectsDuplicateDestinations(t *testing.T) {
	s, b, _ := fixture(t)
	for _, mapping := range []map[string]string{
		{"email": "email", "other": "email"},
		{"email": "email", "first": "fields.age", "second": "fields.age"},
		{"email": "email", "first": "fields", "second": "fields"},
	} {
		result, err := s.ImportCSV(context.Background(), strings.NewReader("email,other,first,second\na@example.org,b@example.org,1,2\n"), ImportOptions{BrandID: b.ID, Mapping: mapping})
		if err == nil || !strings.Contains(err.Error(), "duplicate CSV mapping") || result.Rows != 0 || result.Imported != 0 {
			t.Fatalf("duplicate: %+v %v", result, err)
		}
	}
	result, err := s.ImportCSV(context.Background(), strings.NewReader("email,email\na@example.org,b@example.org\n"), ImportOptions{BrandID: b.ID})
	if err == nil || result.Imported != 0 {
		t.Fatalf("duplicate default mapping accepted: %+v %v", result, err)
	}
}

type csvImportErrorReader struct {
	data io.Reader
	err  error
}

func (r csvImportErrorReader) Read(p []byte) (int, error) {
	n, err := r.data.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}

func TestImportCSVLateReadErrorRetainsCommittedBatches(t *testing.T) {
	s, b, _ := fixture(t)
	sentinel := errors.New("source disconnected")
	input := "email\na@example.org\nb@example.org\nc@example.org\n"
	result, err := s.ImportCSV(context.Background(), csvImportErrorReader{strings.NewReader(input), sentinel}, ImportOptions{BrandID: b.ID, BatchSize: 2})
	if !errors.Is(err, sentinel) || result.Imported != 2 || len(result.Preview) != 3 {
		t.Fatalf("partial import: %+v %v", result, err)
	}
	subs, err := s.Search(context.Background(), b.ID, 0, 100)
	if err != nil || len(subs) != 2 {
		t.Fatalf("committed: %+v %v", subs, err)
	}
	if _, err := s.GetByEmail(context.Background(), b.ID, "c@example.org"); err == nil {
		t.Fatal("pending batch was committed after read failure")
	}
}

func TestImportCSVRetainsBoundedPreviewAndErrors(t *testing.T) {
	s, b, _ := fixture(t)
	var input strings.Builder
	input.WriteString("email\n")
	for i := 0; i < 101; i++ {
		fmt.Fprintf(&input, "reader%d@example.org\ninvalid\n", i)
	}
	result, err := s.ImportCSV(context.Background(), strings.NewReader(input.String()), ImportOptions{BrandID: b.ID, BatchSize: 7})
	if err != nil || result.Rows != 202 || result.Imported != 101 || result.Invalid != 101 || len(result.Preview) != 20 || len(result.Errors) != 20 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestImportCSVLateBatchErrorRollsBackFailingBatch(t *testing.T) {
	s, b, _ := fixture(t)
	if _, err := s.db.Writer.Exec("CREATE TRIGGER reject_import BEFORE INSERT ON subscribers WHEN NEW.email='d@example.org' BEGIN SELECT RAISE(ABORT, 'rejected subscriber'); END"); err != nil {
		t.Fatal(err)
	}
	result, err := s.ImportCSV(context.Background(), strings.NewReader("email\na@example.org\nb@example.org\nc@example.org\nd@example.org\n"), ImportOptions{BrandID: b.ID, BatchSize: 2})
	if err == nil || !strings.Contains(err.Error(), "rejected subscriber") || result.Imported != 2 || result.Rows != 4 {
		t.Fatalf("partial import=%+v error=%v", result, err)
	}
	subs, err := s.Search(context.Background(), b.ID, 0, 100)
	if err != nil || len(subs) != 2 {
		t.Fatalf("committed=%+v error=%v", subs, err)
	}
	if _, err := s.GetByEmail(context.Background(), b.ID, "c@example.org"); err == nil {
		t.Fatal("failed batch was not rolled back")
	}
}
