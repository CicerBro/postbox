package events

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"postbox/internal/store"
	"postbox/internal/subscribers"
)

// SyncBrandSuppressions imports this account's suppression list only into the
// requesting brand. AddSuppression also skips that brand's pending recipients.
func SyncBrandSuppressions(ctx context.Context, db *store.DB, client SESAdmin, brandID int64) error {
	return SyncBrandSuppressionsChecked(ctx, db, client, brandID, nil)
}

// SyncBrandSuppressionsChecked rejects credentials changed while the remote
// suppression list was being read, before each local insertion commits.
func SyncBrandSuppressionsChecked(ctx context.Context, db *store.DB, client SESAdmin, brandID int64, check func(*sql.Conn) error) error {
	if brandID <= 0 {
		return errors.New("suppression sync requires a brand")
	}
	svc := subscribers.New(db)
	var next *string
	for {
		out, err := client.ListSuppressedDestinations(ctx, &sesv2.ListSuppressedDestinationsInput{NextToken: next, PageSize: aws.Int32(1000)})
		if err != nil {
			return err
		}
		for _, dest := range out.SuppressedDestinationSummaries {
			email := strings.ToLower(strings.TrimSpace(aws.ToString(dest.EmailAddress)))
			if email == "" {
				continue
			}
			if err = svc.AddSuppressionChecked(ctx, subscribers.Suppression{BrandID: &brandID, Kind: "email", Value: email, Reason: "ses_sync"}, check); err != nil {
				return err
			}
		}
		next = out.NextToken
		if next == nil || *next == "" {
			return nil
		}
	}
}
