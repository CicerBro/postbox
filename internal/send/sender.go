// Package send implements the durable SQLite campaign pipeline.
package send

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
)

type Quota struct {
	MaxSendRate     float64 `json:"max_send_rate"`
	Max24HourSend   float64 `json:"max_24_hour_send"`
	SentLast24Hours float64 `json:"sent_last_24_hours"`
}
type Request struct {
	From, To                             string
	Raw                                  []byte
	ConfigurationSet                     string
	BrandID, CampaignID                  int64
	CampaignIdentity, SubscriberIdentity string
	Attempt                              int
	Purpose                              string
}

const TestPurpose = "test"

type Sender interface {
	GetAccount(context.Context) (Quota, error)
	Send(context.Context, Request) (string, error)
}
type AWS struct{ Client *sesv2.Client }

// NewAWS uses the standard AWS credential chain and one shared keep-alive transport.
// SDK retries are disabled: retrying a request without journaling it risks duplicates.
func NewAWS(ctx context.Context, region, endpoint string, maxWorkers int) (*AWS, error) {
	if maxWorkers < 1 {
		maxWorkers = 1024
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = maxWorkers * 2
	tr.MaxIdleConnsPerHost = maxWorkers
	tr.MaxConnsPerHost = maxWorkers
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithHTTPClient(&http.Client{Transport: tr, Timeout: 30 * time.Second}), config.WithRetryer(func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 1; o.RateLimiter = ratelimit.None })
	}))
	if err != nil {
		return nil, err
	}
	c := sesv2.NewFromConfig(cfg, func(o *sesv2.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return &AWS{c}, nil
}
func (s *AWS) GetAccount(ctx context.Context) (Quota, error) {
	a, e := s.Client.GetAccount(ctx, &sesv2.GetAccountInput{})
	if e != nil {
		return Quota{}, e
	}
	if a.SendQuota == nil {
		return Quota{}, errors.New("SES returned no sending quota")
	}
	if !a.SendingEnabled {
		return Quota{}, errors.New("SES account sending is disabled")
	}
	return Quota{a.SendQuota.MaxSendRate, a.SendQuota.Max24HourSend, a.SendQuota.SentLast24Hours}, nil
}
func (s *AWS) Send(ctx context.Context, r Request) (string, error) {
	in := &sesv2.SendEmailInput{FromEmailAddress: aws.String(r.From), Destination: &types.Destination{ToAddresses: []string{r.To}}, Content: &types.EmailContent{Raw: &types.RawMessage{Data: r.Raw}}, EmailTags: []types.MessageTag{{Name: aws.String("brand_id"), Value: aws.String(strconv.FormatInt(r.BrandID, 10))}}}
	if r.CampaignID > 0 && r.Purpose != TestPurpose {
		in.EmailTags = append(in.EmailTags, types.MessageTag{Name: aws.String("campaign_id"), Value: aws.String(strconv.FormatInt(r.CampaignID, 10))})
		if r.CampaignIdentity != "" && r.SubscriberIdentity != "" && r.Attempt > 0 {
			in.EmailTags = append(in.EmailTags, types.MessageTag{Name: aws.String("campaign_identity"), Value: aws.String(r.CampaignIdentity)}, types.MessageTag{Name: aws.String("subscriber_identity"), Value: aws.String(r.SubscriberIdentity)}, types.MessageTag{Name: aws.String("send_attempt"), Value: aws.String(strconv.Itoa(r.Attempt))})
		}
	}
	if r.Purpose != "" {
		in.EmailTags = append(in.EmailTags, types.MessageTag{Name: aws.String("message_purpose"), Value: aws.String(r.Purpose)})
	}
	if r.ConfigurationSet != "" {
		in.ConfigurationSetName = aws.String(r.ConfigurationSet)
	}
	out, e := s.Client.SendEmail(ctx, in, func(o *sesv2.Options) {
		// Every dispatch must have its own durable journal entry, even when a
		// caller supplies an SES client configured with the SDK's default retries.
		o.Retryer = retry.NewStandard(func(r *retry.StandardOptions) {
			r.MaxAttempts = 1
			r.RateLimiter = ratelimit.None
		})
	})
	if e != nil {
		return "", e
	}
	return aws.ToString(out.MessageId), nil
}

type ErrorClass string

const (
	Permanent    ErrorClass = "permanent"
	PauseAccount ErrorClass = "pause"
	Throttled    ErrorClass = "throttled"
	Retryable    ErrorClass = "retryable"
	Ambiguous    ErrorClass = "unknown"
)

func Classify(err error) ErrorClass {
	if err == nil {
		return ""
	}
	var a smithy.APIError
	if errors.As(err, &a) {
		switch a.ErrorCode() {
		case "TooManyRequestsException", "Throttling", "ThrottlingException":
			return Throttled
		case "MessageRejected", "MessageRejectedException", "BadRequestException", "InvalidParameterValue":
			return Permanent
		case "AccountSuspendedException", "SendingPausedException", "MailFromDomainNotVerifiedException":
			return PauseAccount
		}
		if a.ErrorFault() == smithy.FaultServer {
			return Ambiguous
		}
		return Permanent
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return Retryable
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return Ambiguous
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Ambiguous
	}
	return Ambiguous
}
