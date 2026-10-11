package awsbrand

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// EventSetupError contains only safe, actionable messages, never upstream errors.
type EventSetupError struct {
	Message  string
	Invalid  bool
	Conflict bool
}

func (e *EventSetupError) Error() string { return e.Message }
func eventFailure(message string) error  { return &EventSetupError{Message: message} }

type EventStatus struct {
	Configured         bool   `json:"configured"`
	ConfigurationSet   string `json:"configuration_set"`
	TopicARN           string `json:"sns_topic_arn"`
	Endpoint           string `json:"endpoint"`
	SubscriptionStatus string `json:"subscription_status"`
	CheckedAt          int64  `json:"checked_at"`
}

// Use only the server's trusted origin. Browsers cannot choose a subscriber URL.
func eventEndpoint(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.EqualFold(u.Hostname(), "localhost") {
		return "", &EventSetupError{Message: "Set POSTBOX_BASE_URL to this deployment's public HTTPS origin before setting up events.", Invalid: true}
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()) {
		return "", &EventSetupError{Message: "SNS needs a public HTTPS endpoint.", Invalid: true}
	}
	u.Path = "/events/sns"
	return u.String(), nil
}

func snsClient(v cached) *sns.Client {
	o := v.client.Client.Options()
	return sns.New(sns.Options{Region: o.Region, Credentials: o.Credentials, HTTPClient: o.HTTPClient, Retryer: o.Retryer, BaseEndpoint: o.BaseEndpoint})
}

func (p *Provider) eventMetadata(ctx context.Context, id int64, baseURL string) (EventStatus, error) {
	out := EventStatus{SubscriptionStatus: "not_subscribed", CheckedAt: time.Now().UnixMilli()}
	var err error
	out.Endpoint, err = eventEndpoint(baseURL)
	if err != nil {
		return out, err
	}
	settings, err := p.Settings(ctx, id)
	if err != nil {
		return out, eventFailure("Brand AWS settings could not be loaded.")
	}
	if !settings.HasCredentials {
		return out, &EventSetupError{Message: "Save this brand's AWS access keys before setting up events.", Invalid: true}
	}
	out.TopicARN = settings.TopicARN
	err = p.db.Reader.QueryRowContext(ctx, "SELECT configuration_set FROM brands WHERE id=?", id).Scan(&out.ConfigurationSet)
	if err != nil {
		return out, eventFailure("Brand configuration set could not be loaded.")
	}
	return out, nil
}

func (p *Provider) eventConnection(ctx context.Context, id int64, topic string) (cached, error) {
	r, err := read(ctx, p.db.Reader, id)
	if err != nil {
		return cached{}, err
	}
	v, err := p.resolve(ctx, id)
	if err != nil {
		return cached{}, err
	}
	if r.TopicARN != topic || fingerprint(r) != v.fingerprint {
		return cached{}, &EventSetupError{Message: "AWS settings changed. Reload settings and retry.", Conflict: true}
	}
	return v, nil
}

// ProvisionEvents reuses resources and preserves unrelated policy statements.
// Partial setup is intentionally retained so a retry can finish it.
func (p *Provider) ProvisionEvents(ctx context.Context, id int64, baseURL string) (EventStatus, error) {
	out, err := p.eventMetadata(ctx, id, baseURL)
	if err != nil {
		return out, err
	}
	originalName := out.ConfigurationSet
	v, err := p.eventConnection(ctx, id, out.TopicARN)
	if err != nil {
		var safe *EventSetupError
		if errors.As(err, &safe) {
			return out, safe
		}
		return out, eventFailure("AWS connection failed. Check this brand's saved access keys and region.")
	}
	region := v.client.Client.Options().Region
	account := strings.Split(v.scope, ":")[0]
	digest := sha256.Sum256([]byte(out.Endpoint))
	name := fmt.Sprintf("postbox-%x-brand-%d", digest[:6], id)
	if out.ConfigurationSet == "" {
		out.ConfigurationSet = name
	}
	client := snsClient(v)
	if out.TopicARN == "" {
		created, e := client.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name + "-events")})
		if e != nil {
			return out, eventFailure("SNS topic creation failed. Grant sns:CreateTopic on the Postbox topic names.")
		}
		out.TopicARN = aws.ToString(created.TopicArn)
	}
	parts := strings.Split(out.TopicARN, ":")
	if !validTopicARN(out.TopicARN, region) || parts[4] != account || strings.HasSuffix(parts[5], ".fifo") {
		return out, &EventSetupError{Message: "Use a Standard SNS topic owned by this brand's AWS account in its SES region.", Invalid: true}
	}
	// Persist routing before Subscribe sends its confirmation callback. Guard against
	// saving resources from a stale connection or overwriting a brand editor's change.
	err = p.db.Immediate(ctx, func(c *sql.Conn) error {
		if e := p.CheckSnapshot(ctx, c, id, v.fingerprint); e != nil {
			return e
		}
		var current string
		if e := c.QueryRowContext(ctx, "SELECT configuration_set FROM brands WHERE id=?", id).Scan(&current); e != nil {
			return e
		}
		if current != originalName {
			return errors.New("configuration changed")
		}
		if _, e := c.ExecContext(ctx, "UPDATE brand_aws_settings SET sns_topic_arn=?,revision=revision+1,updated_at=? WHERE brand_id=?", out.TopicARN, time.Now().UnixMilli(), id); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "UPDATE brands SET configuration_set=? WHERE id=?", out.ConfigurationSet, id); e != nil {
			return e
		}
		r, e := read(ctx, c, id)
		if e == nil {
			v.fingerprint = fingerprint(r)
		}
		return e
	})
	if err != nil {
		return out, &EventSetupError{Message: "AWS or brand settings changed during setup, or this topic is assigned to another brand. Reload settings and retry.", Conflict: true}
	}
	p.mu.Lock()
	delete(p.clients, id)
	p.mu.Unlock()
	attrs, err := client.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(out.TopicARN)})
	if err != nil {
		return out, eventFailure("SNS topic could not be read. Grant sns:GetTopicAttributes on this topic.")
	}
	if attrs.Attributes["Owner"] != account || attrs.Attributes["FifoTopic"] == "true" {
		return out, &EventSetupError{Message: "Use a Standard SNS topic owned by this brand's AWS account.", Invalid: true}
	}
	if err = p.checkEventSnapshot(ctx, id, v.fingerprint, out.ConfigurationSet); err != nil {
		return out, err
	}
	source := fmt.Sprintf("arn:%s:ses:%s:%s:configuration-set/%s", parts[1], region, account, out.ConfigurationSet)
	policy, err := eventTopicPolicy(attrs.Attributes["Policy"], out.TopicARN, source, account, name)
	if err != nil {
		return out, eventFailure("SNS topic policy is invalid. Repair its JSON in AWS, then retry setup.")
	}
	if _, err = client.SetTopicAttributes(ctx, &sns.SetTopicAttributesInput{TopicArn: aws.String(out.TopicARN), AttributeName: aws.String("Policy"), AttributeValue: aws.String(policy)}); err != nil {
		return out, eventFailure("SNS publish policy could not be saved. Grant sns:SetTopicAttributes on this topic.")
	}
	if err = setupEventDestination(ctx, v.client.Client, out.ConfigurationSet, out.TopicARN); err != nil {
		return out, eventFailure("SES event setup failed. Grant ses:CreateConfigurationSet, ses:GetConfigurationSetEventDestinations, ses:CreateConfigurationSetEventDestination and ses:UpdateConfigurationSetEventDestination for this configuration set.")
	}
	out.Configured = true
	if err = p.checkEventSnapshot(ctx, id, v.fingerprint, out.ConfigurationSet); err != nil {
		return out, err
	}
	arn, err := eventSubscription(ctx, client, out.TopicARN, out.Endpoint)
	if err != nil {
		return out, eventFailure("SNS subscriptions could not be read. Grant sns:ListSubscriptionsByTopic on this topic.")
	}
	if arn != "" && arn != "PendingConfirmation" {
		// Existing subscriptions must retain the signed envelope and deliver every event.
		for _, attr := range []struct{ name, value string }{{"RawMessageDelivery", "false"}, {"FilterPolicy", "{}"}} {
			if _, err = client.SetSubscriptionAttributes(ctx, &sns.SetSubscriptionAttributesInput{SubscriptionArn: aws.String(arn), AttributeName: aws.String(attr.name), AttributeValue: aws.String(attr.value)}); err != nil {
				return out, eventFailure("SNS subscription could not be configured. Grant sns:SetSubscriptionAttributes on this topic, using the subscription owner's keys.")
			}
		}
		out.SubscriptionStatus = "confirmed"
	} else {
		sub, e := client.Subscribe(ctx, &sns.SubscribeInput{TopicArn: aws.String(out.TopicARN), Protocol: aws.String("https"), Endpoint: aws.String(out.Endpoint), Attributes: map[string]string{"RawMessageDelivery": "false"}})
		if e != nil {
			return out, eventFailure("SNS subscription failed. Grant sns:Subscribe on this topic for the Postbox HTTPS endpoint.")
		}
		out.SubscriptionStatus = "pending_confirmation"
		if a := aws.ToString(sub.SubscriptionArn); a != "" && a != "pending confirmation" && a != "PendingConfirmation" {
			out.SubscriptionStatus = "confirmed"
		}
	}
	if err = p.checkEventSnapshot(ctx, id, v.fingerprint, out.ConfigurationSet); err != nil {
		return out, err
	}
	out.CheckedAt = time.Now().UnixMilli()
	return out, nil
}

func (p *Provider) checkEventSnapshot(ctx context.Context, id int64, snapshot, name string) error {
	err := p.db.Immediate(ctx, func(c *sql.Conn) error {
		if e := p.CheckSnapshot(ctx, c, id, snapshot); e != nil {
			return e
		}
		var current string
		if e := c.QueryRowContext(ctx, "SELECT configuration_set FROM brands WHERE id=?", id).Scan(&current); e != nil {
			return e
		}
		if current != name {
			return errors.New("configuration changed")
		}
		return nil
	})
	if err != nil {
		return &EventSetupError{Message: "AWS or brand settings changed during setup. Reload settings and retry.", Conflict: true}
	}
	return nil
}

// EventStatus performs only reads, including SNS's actual confirmation state.
func (p *Provider) EventStatus(ctx context.Context, id int64, baseURL string) (EventStatus, error) {
	out, err := p.eventMetadata(ctx, id, baseURL)
	if err != nil || out.TopicARN == "" || out.ConfigurationSet == "" {
		return out, err
	}
	v, err := p.eventConnection(ctx, id, out.TopicARN)
	if err != nil {
		var safe *EventSetupError
		if errors.As(err, &safe) {
			return out, safe
		}
		return out, eventFailure("AWS event status could not be read. Check this brand's saved access keys and region.")
	}
	dest, err := v.client.Client.GetConfigurationSetEventDestinations(ctx, &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: aws.String(out.ConfigurationSet)})
	if err != nil {
		return out, eventFailure("SES event status could not be read. Grant ses:GetConfigurationSetEventDestinations on this configuration set.")
	}
	for _, d := range dest.EventDestinations {
		if aws.ToString(d.Name) == "postbox" && d.Enabled && d.SnsDestination != nil && aws.ToString(d.SnsDestination.TopicArn) == out.TopicARN {
			out.Configured = true
		}
	}
	arn, err := eventSubscription(ctx, snsClient(v), out.TopicARN, out.Endpoint)
	if err != nil {
		return out, eventFailure("SNS event status could not be read. Grant sns:ListSubscriptionsByTopic on this topic.")
	}
	if arn == "PendingConfirmation" {
		out.SubscriptionStatus = "pending_confirmation"
	} else if arn != "" {
		out.SubscriptionStatus = "confirmed"
	}
	if err = p.checkEventSnapshot(ctx, id, v.fingerprint, out.ConfigurationSet); err != nil {
		return out, err
	}
	out.CheckedAt = time.Now().UnixMilli()
	return out, nil
}

func eventSubscription(ctx context.Context, client *sns.Client, topic, endpoint string) (string, error) {
	pages := sns.NewListSubscriptionsByTopicPaginator(client, &sns.ListSubscriptionsByTopicInput{TopicArn: aws.String(topic)}, func(o *sns.ListSubscriptionsByTopicPaginatorOptions) { o.StopOnDuplicateToken = true })
	pending := ""
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, s := range page.Subscriptions {
			if aws.ToString(s.Protocol) == "https" && aws.ToString(s.Endpoint) == endpoint && aws.ToString(s.TopicArn) == topic {
				arn := aws.ToString(s.SubscriptionArn)
				if arn == "PendingConfirmation" {
					pending = arn
				} else if arn != "" && arn != "Deleted" {
					return arn, nil
				}
			}
		}
	}
	return pending, nil
}

func eventTopicPolicy(existing, topic, source, account, owner string) (string, error) {
	policy := map[string]json.RawMessage{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &policy); err != nil || policy == nil {
			return "", errors.New("invalid policy")
		}
	}
	statements := []json.RawMessage{}
	if raw, ok := policy["Statement"]; ok {
		if err := json.Unmarshal(raw, &statements); err != nil {
			var single map[string]json.RawMessage
			if err = json.Unmarshal(raw, &single); err != nil || single == nil {
				return "", errors.New("invalid statements")
			}
			statements = []json.RawMessage{raw}
		} else if statements == nil {
			return "", errors.New("invalid statements")
		}
	}
	digest := sha256.Sum256([]byte(owner))
	sid := fmt.Sprintf("PostboxSESPublish%x", digest[:8])
	kept := []json.RawMessage{}
	for _, raw := range statements {
		var s map[string]json.RawMessage
		if err := json.Unmarshal(raw, &s); err != nil || s == nil {
			return "", errors.New("invalid statement")
		}
		var oldSID string
		_ = json.Unmarshal(s["Sid"], &oldSID)
		if oldSID != sid {
			kept = append(kept, raw)
		}
	}
	owned, err := json.Marshal(map[string]any{"Sid": sid, "Effect": "Allow", "Principal": map[string]string{"Service": "ses.amazonaws.com"}, "Action": "sns:Publish", "Resource": topic, "Condition": map[string]any{"StringEquals": map[string]string{"AWS:SourceAccount": account, "AWS:SourceArn": source}}})
	if err != nil {
		return "", err
	}
	kept = append(kept, owned)
	policy["Statement"], err = json.Marshal(kept)
	if err != nil {
		return "", err
	}
	if _, ok := policy["Version"]; !ok {
		policy["Version"] = json.RawMessage(`"2012-10-17"`)
	}
	data, err := json.Marshal(policy)
	return string(data), err
}
