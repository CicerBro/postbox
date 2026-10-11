package awsbrand

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"postbox/internal/send"
)

type Status struct {
	CustomEndpoint          bool       `json:"custom_endpoint"`
	AccountID               string     `json:"account_id"`
	Region                  string     `json:"region"`
	SendingEnabled          bool       `json:"sending_enabled"`
	ProductionAccessEnabled bool       `json:"production_access_enabled"`
	EnforcementStatus       string     `json:"enforcement_status"`
	Quota                   send.Quota `json:"quota"`
	Identities              []Identity `json:"identities"`
}
type Identity struct {
	Identity           string   `json:"identity"`
	IdentityType       string   `json:"identity_type"`
	SendingEnabled     bool     `json:"sending_enabled"`
	VerificationStatus string   `json:"verification_status"`
	DKIMTokens         []string `json:"dkim_tokens,omitempty"`
	DKIMStatus         string   `json:"dkim_status,omitempty"`
	MailFromDomain     string   `json:"mail_from_domain,omitempty"`
	MailFromStatus     string   `json:"mail_from_status,omitempty"`
	MailFromBehavior   string   `json:"mail_from_behavior,omitempty"`
	DKIMOrigin         string   `json:"dkim_origin,omitempty"`
	DKIMSigningEnabled bool     `json:"dkim_signing_enabled,omitempty"`
	DKIMHostedZone     string   `json:"dkim_hosted_zone,omitempty"`
	Region             string   `json:"region,omitempty"`
	CustomEndpoint     bool     `json:"custom_endpoint,omitempty"`
}

var ErrIdentityNotFound = errors.New("SES identity not found")

func (p *Provider) SESClient(ctx context.Context, id int64) (*sesv2.Client, error) {
	v, e := p.resolve(ctx, id)
	if e != nil {
		return nil, e
	}
	return v.client.Client, nil
}

// SESClientSnapshot captures a client and its connection guard in one resolution.
func (p *Provider) SESClientSnapshot(ctx context.Context, id int64) (*sesv2.Client, string, error) {
	v, e := p.resolve(ctx, id)
	if e != nil {
		return nil, "", e
	}
	return v.client.Client, v.fingerprint, nil
}
func (p *Provider) Status(ctx context.Context, id int64) (Status, error) {
	v, e := p.resolve(ctx, id)
	if e != nil {
		return Status{}, e
	}
	a, e := v.client.Client.GetAccount(ctx, &sesv2.GetAccountInput{})
	if e != nil {
		return Status{}, awsCheckError("ses:GetAccount", v.client.Client.Options().Region, e)
	}
	if a.SendQuota == nil {
		return Status{}, errors.New("SES returned no sending quota")
	}
	for _, value := range []float64{a.SendQuota.MaxSendRate, a.SendQuota.Max24HourSend, a.SendQuota.SentLast24Hours} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Status{}, errors.New("SES returned an invalid sending quota")
		}
	}
	parts := strings.SplitN(v.scope, ":", 2)
	out := Status{CustomEndpoint: v.client.Client.Options().BaseEndpoint != nil, AccountID: parts[0], Region: parts[1], SendingEnabled: a.SendingEnabled, ProductionAccessEnabled: a.ProductionAccessEnabled, EnforcementStatus: aws.ToString(a.EnforcementStatus), Identities: []Identity{}}
	if a.SendQuota != nil {
		out.Quota = send.Quota{MaxSendRate: a.SendQuota.MaxSendRate, Max24HourSend: a.SendQuota.Max24HourSend, SentLast24Hours: a.SendQuota.SentLast24Hours}
	}
	pager := sesv2.NewListEmailIdentitiesPaginator(v.client.Client, &sesv2.ListEmailIdentitiesInput{})
	for pager.HasMorePages() {
		page, e := pager.NextPage(ctx)
		if e != nil {
			return Status{}, awsCheckError("ses:ListEmailIdentities", v.client.Client.Options().Region, e)
		}
		for _, i := range page.EmailIdentities {
			out.Identities = append(out.Identities, Identity{Identity: aws.ToString(i.IdentityName), IdentityType: string(i.IdentityType), SendingEnabled: i.SendingEnabled, VerificationStatus: string(i.VerificationStatus)})
		}
	}
	return out, nil
}
func identity(name string, a *sesv2.GetEmailIdentityOutput) Identity {
	out := Identity{Identity: name, IdentityType: string(a.IdentityType), SendingEnabled: a.VerifiedForSendingStatus, VerificationStatus: string(a.VerificationStatus)}
	if a.DkimAttributes != nil {
		out.DKIMTokens = a.DkimAttributes.Tokens
		out.DKIMStatus = string(a.DkimAttributes.Status)
		out.DKIMOrigin = string(a.DkimAttributes.SigningAttributesOrigin)
		out.DKIMSigningEnabled = a.DkimAttributes.SigningEnabled
		out.DKIMHostedZone = aws.ToString(a.DkimAttributes.SigningHostedZone)
	}
	if a.MailFromAttributes != nil {
		out.MailFromDomain = aws.ToString(a.MailFromAttributes.MailFromDomain)
		out.MailFromStatus = string(a.MailFromAttributes.MailFromDomainStatus)
		out.MailFromBehavior = string(a.MailFromAttributes.BehaviorOnMxFailure)
	}
	return out
}
func (p *Provider) GetIdentity(ctx context.Context, id int64, name string) (Identity, error) {
	c, e := p.SESClient(ctx, id)
	if e != nil {
		return Identity{}, e
	}
	return getIdentity(ctx, c, name)
}

func getIdentity(ctx context.Context, c *sesv2.Client, name string) (Identity, error) {
	a, e := c.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{EmailIdentity: aws.String(name)})
	if e != nil {
		var missing *types.NotFoundException
		if errors.As(e, &missing) {
			return Identity{}, ErrIdentityNotFound
		}
		return Identity{}, errors.New("SES identity could not be retrieved")
	}
	out := identity(name, a)
	out.Region = c.Options().Region
	out.CustomEndpoint = c.Options().BaseEndpoint != nil
	return out, nil
}
func (p *Provider) VerifyIdentity(ctx context.Context, id int64, name string) (Identity, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Identity{}, errors.New("an email address or domain is required")
	}
	c, e := p.SESClient(ctx, id)
	if e != nil {
		return Identity{}, e
	}
	a, e := c.CreateEmailIdentity(ctx, &sesv2.CreateEmailIdentityInput{EmailIdentity: aws.String(name)})
	if e != nil {
		var exists *types.AlreadyExistsException
		if errors.As(e, &exists) {
			return getIdentity(ctx, c, name)
		}
		return Identity{}, errors.New("SES identity verification could not be requested")
	}
	status := types.VerificationStatusPending
	if a.VerifiedForSendingStatus {
		status = types.VerificationStatusSuccess
	}
	out := identity(name, &sesv2.GetEmailIdentityOutput{IdentityType: a.IdentityType, VerifiedForSendingStatus: a.VerifiedForSendingStatus, VerificationStatus: status, DkimAttributes: a.DkimAttributes})
	out.Region = c.Options().Region
	out.CustomEndpoint = c.Options().BaseEndpoint != nil
	return out, nil
}
func (p *Provider) DeleteIdentity(ctx context.Context, id int64, name string) error {
	c, e := p.SESClient(ctx, id)
	if e != nil {
		return e
	}
	_, e = c.DeleteEmailIdentity(ctx, &sesv2.DeleteEmailIdentityInput{EmailIdentity: aws.String(name)})
	if e != nil {
		return errors.New("SES identity could not be deleted")
	}
	return nil
}
func (p *Provider) SetMailFrom(ctx context.Context, id int64, name, domain, behavior string) error {
	c, e := p.SESClient(ctx, id)
	if e != nil {
		return e
	}
	in := &sesv2.PutEmailIdentityMailFromAttributesInput{EmailIdentity: aws.String(name), BehaviorOnMxFailure: types.BehaviorOnMxFailure(behavior)}
	if domain != "" {
		in.MailFromDomain = aws.String(domain)
	}
	_, e = c.PutEmailIdentityMailFromAttributes(ctx, in)
	if e != nil {
		return errors.New("SES mail-from domain could not be updated")
	}
	return nil
}
