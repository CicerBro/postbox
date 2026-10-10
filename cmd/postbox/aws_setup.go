package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
	"postbox/internal/config"
)

// setupSES configures an existing SNS topic. Topic/queue resource policies are
// deliberately managed by infrastructure provisioning, not the runtime role.
func setupSES(ctx context.Context, c config.Config) error {
	name := os.Getenv("POSTBOX_SES_CONFIGURATION_SET")
	if name == "" || c.TopicARN == "" {
		return errors.New("set POSTBOX_SES_CONFIGURATION_SET and POSTBOX_SNS_TOPIC_ARN for setup-ses")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region))
	if err != nil {
		return err
	}
	client := sesv2.NewFromConfig(cfg)
	if _, err = client.GetConfigurationSet(ctx, &sesv2.GetConfigurationSetInput{ConfigurationSetName: aws.String(name)}); err != nil {
		var api smithy.APIError
		if !errors.As(err, &api) || api.ErrorCode() != "NotFoundException" {
			return err
		}
		if _, err = client.CreateConfigurationSet(ctx, &sesv2.CreateConfigurationSetInput{ConfigurationSetName: aws.String(name)}); err != nil {
			return err
		}
	}
	output, err := client.GetConfigurationSetEventDestinations(ctx, &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: aws.String(name)})
	if err != nil {
		return err
	}
	destination := &types.EventDestinationDefinition{Enabled: true, MatchingEventTypes: []types.EventType{types.EventTypeSend, types.EventTypeDelivery, types.EventTypeBounce, types.EventTypeComplaint, types.EventTypeReject, types.EventTypeRenderingFailure, types.EventTypeDeliveryDelay}, SnsDestination: &types.SnsDestination{TopicArn: aws.String(c.TopicARN)}}
	exists := false
	for _, d := range output.EventDestinations {
		if aws.ToString(d.Name) == "postbox" {
			exists = true
		}
	}
	if exists {
		_, err = client.UpdateConfigurationSetEventDestination(ctx, &sesv2.UpdateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
	} else {
		_, err = client.CreateConfigurationSetEventDestination(ctx, &sesv2.CreateConfigurationSetEventDestinationInput{ConfigurationSetName: aws.String(name), EventDestinationName: aws.String("postbox"), EventDestination: destination})
	}
	if err != nil {
		return err
	}
	fmt.Printf("SES configuration set %s sends events to %s\n", name, c.TopicARN)
	return nil
}
