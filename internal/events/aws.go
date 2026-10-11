package events

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

type SESAdmin interface {
	ListSuppressedDestinations(context.Context, *sesv2.ListSuppressedDestinationsInput, ...func(*sesv2.Options)) (*sesv2.ListSuppressedDestinationsOutput, error)
}
