package events

import (
	"context"
	"database/sql"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"postbox/internal/store"
	"strings"
	"time"
)

type SESAdmin interface {
	ListSuppressedDestinations(context.Context, *sesv2.ListSuppressedDestinationsInput, ...func(*sesv2.Options)) (*sesv2.ListSuppressedDestinationsOutput, error)
	GetConfigurationSet(context.Context, *sesv2.GetConfigurationSetInput, ...func(*sesv2.Options)) (*sesv2.GetConfigurationSetOutput, error)
	CreateConfigurationSet(context.Context, *sesv2.CreateConfigurationSetInput, ...func(*sesv2.Options)) (*sesv2.CreateConfigurationSetOutput, error)
	GetConfigurationSetEventDestinations(context.Context, *sesv2.GetConfigurationSetEventDestinationsInput, ...func(*sesv2.Options)) (*sesv2.GetConfigurationSetEventDestinationsOutput, error)
	CreateConfigurationSetEventDestination(context.Context, *sesv2.CreateConfigurationSetEventDestinationInput, ...func(*sesv2.Options)) (*sesv2.CreateConfigurationSetEventDestinationOutput, error)
	UpdateConfigurationSetEventDestination(context.Context, *sesv2.UpdateConfigurationSetEventDestinationInput, ...func(*sesv2.Options)) (*sesv2.UpdateConfigurationSetEventDestinationOutput, error)
}

func SyncSuppressions(ctx context.Context, db *store.DB, client SESAdmin) error {
	var next *string
	for {
		out, e := client.ListSuppressedDestinations(ctx, &sesv2.ListSuppressedDestinationsInput{NextToken: next, PageSize: aws.Int32(1000)})
		if e != nil {
			return e
		}
		e = db.Immediate(ctx, func(c *sql.Conn) error {
			for _, dest := range out.SuppressedDestinationSummaries {
				email := strings.ToLower(strings.TrimSpace(aws.ToString(dest.EmailAddress)))
				if email == "" {
					continue
				}
				if _, e := c.ExecContext(ctx, `INSERT OR IGNORE INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(NULL,'email',?,'ses_sync',?)`, email, time.Now().UnixMilli()); e != nil {
					return e
				}
				if e := skipEmail(ctx, c, email); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
		next = out.NextToken
		if next == nil || *next == "" {
			return nil
		}
	}
}
func EnsureConfigurationSet(ctx context.Context, client SESAdmin, name, topicARN string) error {
	if name == "" || topicARN == "" {
		return errors.New("configuration set and SNS topic required")
	}
	_, e := client.GetConfigurationSet(ctx, &sesv2.GetConfigurationSetInput{ConfigurationSetName: aws.String(name)})
	if e != nil {
		var missing *types.NotFoundException
		if !errors.As(e, &missing) {
			return e
		}
		if _, e = client.CreateConfigurationSet(ctx, &sesv2.CreateConfigurationSetInput{ConfigurationSetName: aws.String(name)}); e != nil {
			return e
		}
	}
	out, e := client.GetConfigurationSetEventDestinations(ctx, &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: aws.String(name)})
	if e != nil {
		return e
	}
	dest := &types.EventDestinationDefinition{Enabled: true, MatchingEventTypes: []types.EventType{types.EventTypeBounce, types.EventTypeComplaint, types.EventTypeDelivery, types.EventTypeReject, types.EventTypeDeliveryDelay}, SnsDestination: &types.SnsDestination{TopicArn: aws.String(topicARN)}}
	for _, d := range out.EventDestinations {
		if aws.ToString(d.Name) == "postbox-events" {
			_, e = client.UpdateConfigurationSetEventDestination(ctx, &sesv2.UpdateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox-events"), EventDestination: dest})
			return e
		}
	}
	_, e = client.CreateConfigurationSetEventDestination(ctx, &sesv2.CreateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox-events"), EventDestination: dest})
	return e
}
