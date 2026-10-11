package awsbrand

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SetupEvents creates a missing brand SES configuration set and connects it to its SNS topic.
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
	return setupEventDestination(ctx, client, name, settings.TopicARN)
}

func setupEventDestination(ctx context.Context, client *sesv2.Client, name, topic string) error {
	input := &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: aws.String(name)}
	output, err := client.GetConfigurationSetEventDestinations(ctx, input)
	if err != nil {
		var missing *types.NotFoundException
		if !errors.As(err, &missing) {
			return err
		}
		_, err = client.CreateConfigurationSet(ctx, &sesv2.CreateConfigurationSetInput{ConfigurationSetName: aws.String(name)})
		if err != nil {
			var exists *types.AlreadyExistsException
			if !errors.As(err, &exists) {
				return err
			}
		}
		output, err = client.GetConfigurationSetEventDestinations(ctx, input)
		if err != nil {
			return err
		}
	}
	destination := &types.EventDestinationDefinition{
		Enabled:            true,
		MatchingEventTypes: []types.EventType{types.EventTypeSend, types.EventTypeDelivery, types.EventTypeBounce, types.EventTypeComplaint, types.EventTypeReject, types.EventTypeRenderingFailure, types.EventTypeDeliveryDelay},
		SnsDestination:     &types.SnsDestination{TopicArn: aws.String(topic)},
	}
	for _, existing := range output.EventDestinations {
		if aws.ToString(existing.Name) == "postbox" {
			_, err = client.UpdateConfigurationSetEventDestination(ctx, &sesv2.UpdateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
			return err
		}
	}
	_, err = client.CreateConfigurationSetEventDestination(ctx, &sesv2.CreateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
	var exists *types.AlreadyExistsException
	if errors.As(err, &exists) {
		_, err = client.UpdateConfigurationSetEventDestination(ctx, &sesv2.UpdateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
	}
	return err
}
