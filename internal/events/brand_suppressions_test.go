package events

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"postbox/internal/awsbrand"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type brandSuppressionClient struct {
	SESAdmin
	calls int
}

func (c *brandSuppressionClient) ListSuppressedDestinations(_ context.Context, in *sesv2.ListSuppressedDestinationsInput, _ ...func(*sesv2.Options)) (*sesv2.ListSuppressedDestinationsOutput, error) {
	c.calls++
	out := &sesv2.ListSuppressedDestinationsOutput{}
	if c.calls%2 == 1 {
		out.SuppressedDestinationSummaries = []types.SuppressedDestinationSummary{{EmailAddress: aws.String(" READER@example.org ")}}
		out.NextToken = aws.String("next")
	} else if aws.ToString(in.NextToken) != "next" {
		panic("missing pagination token")
	}
	return out, nil
}

func TestBrandSuppressionSyncCannotSuppressAnotherAccountBrand(t *testing.T) {
	db := eventDB(t)
	_, err := db.Writer.Exec(`
UPDATE subscribers SET email='reader@example.org' WHERE id IN(1,2);
UPDATE campaign_recipients SET status='pending' WHERE campaign_id=1 AND subscriber_id=1;`)
	if err != nil {
		t.Fatal(err)
	}
	client := &brandSuppressionClient{}
	for i := 0; i < 2; i++ {
		if err = SyncBrandSuppressions(context.Background(), db, client, 1); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.Reader.QueryRow(`SELECT count(*) FROM suppressions WHERE value='reader@example.org' AND brand_id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("suppression count %d: %v", count, err)
	}
	if err = db.Reader.QueryRow(`SELECT count(*) FROM suppressions WHERE brand_id IS NULL OR brand_id=2`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-brand suppressions %d: %v", count, err)
	}
	var status string
	if err = db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=1 AND subscriber_id=1`).Scan(&status); err != nil || status != "skipped" {
		t.Fatalf("source recipient %s: %v", status, err)
	}
	if err = db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=2 AND subscriber_id=2`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("other recipient %s: %v", status, err)
	}
	if err = db.Reader.QueryRow(`SELECT skipped FROM campaign_stats WHERE campaign_id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("skip count %d: %v", count, err)
	}
	if client.calls != 4 {
		t.Fatalf("page calls=%d", client.calls)
	}
}

func TestChangedCredentialsCannotImportOldAccountSuppressionResponse(t *testing.T) {
	ctx := context.Background()
	db := eventDB(t)
	if _, err := db.Writer.Exec(`UPDATE campaign_recipients SET status='pending' WHERE campaign_id=1; UPDATE subscribers SET email='reader@example.org' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var provider *awsbrand.Provider
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(`<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>arn:aws:iam::111111111111:user/test</Arn><UserId>test</UserId><Account>111111111111</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`))
			return
		}
		// The request used the old client, but a credential edit commits before
		// its response arrives. The transaction guard must reject this import.
		if err := provider.Save(ctx, 1, awsbrand.Update{Mode: "static", Region: "us-east-1", AccessKeyID: "NEW_KEY", SecretAccessKey: "NEW_SECRET"}); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"SuppressedDestinationSummaries":[{"EmailAddress":"reader@example.org","Reason":"BOUNCE"}]}`))
	}))
	defer server.Close()
	var err error
	provider, err = awsbrand.New(db, []byte(strings.Repeat("s", 32)), "us-east-1", server.URL, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.Save(ctx, 1, awsbrand.Update{Mode: "static", Region: "us-east-1", AccessKeyID: "OLD_KEY", SecretAccessKey: "OLD_SECRET"}); err != nil {
		t.Fatal(err)
	}
	client, snapshot, err := provider.SESClientSnapshot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	err = SyncBrandSuppressionsChecked(ctx, db, client, 1, func(conn *sql.Conn) error { return provider.CheckSnapshot(ctx, conn, 1, snapshot) })
	if err == nil {
		t.Fatal("stale SES response was imported")
	}
	var count int
	if err = db.Reader.QueryRow(`SELECT count(*) FROM suppressions WHERE brand_id=1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale suppressions %d: %v", count, err)
	}
	var status string
	if err = db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=1`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("stale import skipped recipient %s: %v", status, err)
	}
}
