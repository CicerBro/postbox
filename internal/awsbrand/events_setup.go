package awsbrand

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SetupEvents connects an existing brand SES configuration set to its SNS topic.
// Topic and queue creation, subscriptions and resource policies remain in AWS.
func (p *Provider) SetupEvents(ctx context.Context, id int64) error {
	settings, err := p.Settings(ctx, id)
	if err != nil {
		return err
	}
	var name string
	if err = p.db.Reader.QueryRowContext(ctx, "SELECT configuration_set FROM brands WHERE id=?", id).Scan(&name); err != nil {
		return err
	}
	if !settings.HasCredentials || settings.TopicARN == "" || name == "" {
		return errors.New("brand keys, SNS topic and SES configuration set are required")
	}
	client, err := p.SESClient(ctx, id)
	if err != nil {
		return err
	}
	output, err := client.GetConfigurationSetEventDestinations(ctx, &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: aws.String(name)})
	if err != nil {
		return err
	}
	destination := &types.EventDestinationDefinition{
		Enabled:            true,
		MatchingEventTypes: []types.EventType{types.EventTypeSend, types.EventTypeDelivery, types.EventTypeBounce, types.EventTypeComplaint, types.EventTypeReject, types.EventTypeRenderingFailure, types.EventTypeDeliveryDelay},
		SnsDestination:     &types.SnsDestination{TopicArn: aws.String(settings.TopicARN)},
	}
	for _, existing := range output.EventDestinations {
		if aws.ToString(existing.Name) == "postbox" {
			_, err = client.UpdateConfigurationSetEventDestination(ctx, &sesv2.UpdateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
			return err
		}
	}
	_, err = client.CreateConfigurationSetEventDestination(ctx, &sesv2.CreateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
	return err
}
