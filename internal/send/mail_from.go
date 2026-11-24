package send

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
	"golang.org/x/net/publicsuffix"
)

type mailFromResolver interface {
	LookupTXT(context.Context, string) ([]string, error)
	LookupMX(context.Context, string) ([]*net.MX, error)
}

// CheckCampaignSender checks the From address before a campaign is queued.
func (e *Engine) CheckCampaignSender(ctx context.Context, id int64) error {
	var brand int64
	var from string
	err := e.db.Reader.QueryRowContext(ctx, `SELECT c.brand_id, coalesce(p.from_email, b.from_email)
		FROM campaigns c JOIN brands b ON b.id=c.brand_id
		LEFT JOIN campaign_preparations p ON p.campaign_id=c.id WHERE c.id=?`, id).Scan(&brand, &from)
	if err != nil {
		return err
	}
	delivery, err := e.resolveBrand(ctx, brand)
	if err != nil {
		return err
	}
	return campaignSenderError(ctx, delivery.sender, from)
}

func campaignSenderError(ctx context.Context, sender Sender, from string) error {
	var messages []string
	for _, issue := range checkCampaignSender(ctx, sender, from, net.DefaultResolver) {
		messages = append(messages, issue.Message)
	}
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, ". "))
}

func checkCampaignSender(ctx context.Context, sender Sender, from string, resolver mailFromResolver) []PreflightIssue {
	ses, ok := sender.(*AWS)
	if !ok {
		return nil
	}
	identityCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	identity, attributes, err := ses.senderIdentity(identityCtx, from)
	cancel()
	if err != nil {
		return []PreflightIssue{{Code: "ses_identity", Message: err.Error()}}
	}
	if attributes == nil || aws.ToString(attributes.MailFromDomain) == "" {
		return nil // SES supplies the MAIL FROM domain and its DNS records.
	}
	domain := strings.TrimSuffix(aws.ToString(attributes.MailFromDomain), ".")
	expectedMX := "feedback-smtp." + ses.Client.Options().Region + ".amazonses.com"
	return checkMailFromDNS(ctx, resolver, identity, domain, expectedMX)
}

func (s *AWS) senderIdentity(ctx context.Context, from string) (string, *types.MailFromAttributes, error) {
	address, err := mail.ParseAddress(from)
	if err != nil {
		return "", nil, fmt.Errorf("invalid SES sender address %q", from)
	}
	at := strings.LastIndexByte(address.Address, '@')
	local, domain := address.Address[:at], address.Address[at+1:]
	candidates := []string{address.Address}
	if base, _, labeled := strings.Cut(local, "+"); labeled && base != "" {
		candidates = append(candidates, base+"@"+domain)
	}
	domain = strings.ToLower(domain)
	root, _ := publicsuffix.EffectiveTLDPlusOne(domain)
	for {
		candidates = append(candidates, domain)
		_, parent, dotted := strings.Cut(domain, ".")
		if domain == root || root == "" || !dotted {
			break
		}
		domain = parent
	}

	// Consecutive identity lookups can hit SES's one-request-per-second quota.
	// Retry this read without changing the single-attempt dispatch policy.
	jitter := retry.NewExponentialJitterBackoff(time.Second)
	readRetryer := retry.NewStandard(func(o *retry.StandardOptions) {
		o.Backoff = retry.BackoffDelayerFunc(func(attempt int, err error) (time.Duration, error) {
			delay, err := jitter.BackoffDelay(attempt, err)
			return time.Second + delay, err
		})
	})
	for _, name := range candidates {
		out, err := s.Client.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{EmailIdentity: aws.String(name)}, func(o *sesv2.Options) {
			o.Retryer = readRetryer
		})
		var missing *types.NotFoundException
		if errors.As(err, &missing) {
			continue
		}
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AccessDeniedException" || apiErr.ErrorCode() == "AccessDenied") {
				return "", nil, fmt.Errorf("SES denied ses:GetEmailIdentity for %q in %s", name, s.Client.Options().Region)
			}
			return "", nil, fmt.Errorf("could not check SES identity %q in %s", name, s.Client.Options().Region)
		}
		if out.VerifiedForSendingStatus && (out.VerificationStatus == "" || out.VerificationStatus == types.VerificationStatusSuccess) {
			return name, out.MailFromAttributes, nil
		}
	}
	return "", nil, fmt.Errorf("no verified SES identity for %q in %s", address.Address, s.Client.Options().Region)
}

func checkMailFromDNS(ctx context.Context, resolver mailFromResolver, identity, domain, expectedMX string) []PreflightIssue {
	var issues []PreflightIssue
	name := domain + "."
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	txt, err := resolver.LookupTXT(lookupCtx, name)
	cancel()
	if err != nil || !hasSESSPF(txt) {
		issues = append(issues, PreflightIssue{Code: "mail_from_spf", Message: fmt.Sprintf(
			"SES identity %q needs one valid SPF TXT record at %s authorizing amazonses.com (for example, v=spf1 include:amazonses.com ~all)%s", identity, domain, dnsLookupNote(err))})
	}
	lookupCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	mx, err := resolver.LookupMX(lookupCtx, name)
	cancel()
	if err != nil || len(mx) != 1 || mx[0].Pref != 10 || !strings.EqualFold(strings.TrimSuffix(mx[0].Host, "."), expectedMX) {
		issues = append(issues, PreflightIssue{Code: "mail_from_mx", Message: fmt.Sprintf(
			"SES identity %q needs exactly one MX record at %s pointing to %s with priority 10%s", identity, domain, expectedMX, dnsLookupNote(err))})
	}
	return issues
}

func dnsLookupNote(err error) string {
	var dnsErr *net.DNSError
	if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		return " (DNS lookup failed; retry preflight)"
	}
	return ""
}

func hasSESSPF(records []string) bool {
	var policy []string
	for _, record := range records {
		terms := strings.Fields(strings.ToLower(record))
		if len(terms) == 0 || terms[0] != "v=spf1" {
			continue
		}
		if policy != nil {
			return false
		}
		policy = terms
	}
	if policy == nil {
		return false
	}
	for _, term := range policy[1:] {
		if strings.TrimLeft(term, "+-~?") == "all" {
			return false
		}
		switch strings.TrimSuffix(term, ".") {
		case "include:amazonses.com", "+include:amazonses.com":
			return true
		case "-include:amazonses.com", "~include:amazonses.com", "?include:amazonses.com":
			return false
		}
	}
	return false
}
