package awsbrand

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type eventAWS struct {
	mu                                                sync.Mutex
	topic, policy, subscription, endpoint, configName string
	config, destination                               bool
	calls                                             map[string]int
	deny                                              string
	beforeSubscribe                                   func()
	duringPolicy                                      func()
}

func (f *eventAWS) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=event-brand-key/") || !strings.Contains(r.Header.Get("Authorization"), "/eu-west-1/") {
			t.Error("wrong brand keys or region", r.Header.Get("Authorization"))
		}
		_ = r.ParseForm()
		action := r.Form.Get("Action")
		if action != "" {
			f.calls[action]++
			w.Header().Set("Content-Type", "text/xml")
			if action == f.deny {
				w.WriteHeader(403)
				fmt.Fprint(w, `<ErrorResponse><Error><Code>AuthorizationError</Code><Message>event-brand-key PRIVATE-SECRET</Message></Error></ErrorResponse>`)
				return
			}
			result := ""
			esc := func(s string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
			switch action {
			case "GetCallerIdentity":
				fmt.Fprint(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
				return
			case "CreateTopic":
				if f.topic == "" {
					f.topic = "arn:aws:sns:eu-west-1:123456789012:" + r.Form.Get("Name")
				}
				result = "<TopicArn>" + f.topic + "</TopicArn>"
			case "GetTopicAttributes":
				if f.duringPolicy != nil {
					callback := f.duringPolicy
					f.duringPolicy = nil
					callback()
				}
				result = "<Attributes><entry><key>Owner</key><value>123456789012</value></entry><entry><key>Policy</key><value>" + esc(f.policy) + "</value></entry></Attributes>"
			case "SetTopicAttributes":
				if r.Form.Get("AttributeName") != "Policy" {
					t.Error("unexpected topic attribute")
				}
				f.policy = r.Form.Get("AttributeValue")
			case "ListSubscriptionsByTopic":
				// A matching subscriber on page two must not be duplicated.
				if r.Form.Get("NextToken") == "" {
					result = "<NextToken>second</NextToken><Subscriptions/>"
				} else if f.subscription != "" {
					result = "<Subscriptions><member><Endpoint>" + esc(f.endpoint) + "</Endpoint><Protocol>https</Protocol><TopicArn>" + f.topic + "</TopicArn><SubscriptionArn>" + f.subscription + "</SubscriptionArn></member></Subscriptions>"
				}
			case "Subscribe":
				if f.beforeSubscribe != nil {
					f.beforeSubscribe()
				}
				if r.Form.Get("Protocol") != "https" {
					t.Error("expected HTTPS")
				}
				f.endpoint = r.Form.Get("Endpoint")
				if f.endpoint != "https://postbox.example.org/events/sns" {
					t.Error("wrong callback", f.endpoint)
				}
				raw := false
				for k, values := range r.Form {
					if strings.HasSuffix(k, ".key") && values[0] == "RawMessageDelivery" {
						raw = r.Form.Get(strings.TrimSuffix(k, "key")+"value") == "false"
					}
				}
				if !raw {
					t.Error("subscription must preserve signed SNS envelope")
				}
				f.subscription = "PendingConfirmation"
				result = "<SubscriptionArn>pending confirmation</SubscriptionArn>"
			case "SetSubscriptionAttributes":
				name, value := r.Form.Get("AttributeName"), r.Form.Get("AttributeValue")
				if (name != "RawMessageDelivery" || value != "false") && (name != "FilterPolicy" || value != "{}") {
					t.Error("unexpected subscription repair", name, value)
				}
			default:
				t.Error("unexpected action", action)
			}
			fmt.Fprintf(w, "<%sResponse><%sResult>%s</%sResult></%sResponse>", action, action, result, action, action)
			return
		}
		key := r.Method + " " + r.URL.Path
		f.calls[key]++
		w.Header().Set("Content-Type", "application/json")
		if key == f.deny {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"__type":"AccessDeniedException","message":"PRIVATE-SECRET"}`)
			return
		}
		switch {
		case r.URL.Path == "/v2/email/configuration-sets" && r.Method == "POST":
			var body struct{ ConfigurationSetName string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.configName = body.ConfigurationSetName
			f.config = true
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/event-destinations") && r.Method == "GET":
			if !f.config {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"__type":"NotFoundException"}`)
			} else if !f.destination {
				fmt.Fprint(w, `{"EventDestinations":[]}`)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"EventDestinations": []any{map[string]any{"Name": "postbox", "Enabled": true, "SnsDestination": map[string]string{"TopicArn": f.topic}}}})
			}
		case (strings.HasSuffix(r.URL.Path, "/event-destinations") && r.Method == "POST") || (strings.HasSuffix(r.URL.Path, "/event-destinations/postbox") && r.Method == "PUT"):
			var body struct {
				EventDestination struct {
					Enabled            bool
					MatchingEventTypes []string
					SnsDestination     struct{ TopicArn string }
				}
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !body.EventDestination.Enabled || len(body.EventDestination.MatchingEventTypes) != 7 || body.EventDestination.SnsDestination.TopicArn != f.topic {
				t.Errorf("wrong destination: %+v", body)
			}
			f.destination = true
			fmt.Fprint(w, `{}`)
		default:
			t.Error("unexpected SES call", key)
			http.Error(w, "unexpected", 404)
		}
	})
}
func provisionFixture(t *testing.T) (*Provider, *eventAWS) {
	t.Helper()
	f := &eventAWS{calls: map[string]int{}, policy: `{"Version":"2012-10-17","Id":"existing","Statement":[{"Sid":"OwnerAccess","Effect":"Allow","Principal":{"AWS":"123456789012"},"Action":"sns:*","Resource":"*"}]}`}
	server := httptest.NewServer(f.handler(t))
	t.Cleanup(server.Close)
	p, db := testProvider(t, server.URL)
	if _, err := db.Writer.Exec("UPDATE brands SET configuration_set='' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(context.Background(), 1, Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "event-brand-key", SecretAccessKey: "PRIVATE-SECRET"}); err != nil {
		t.Fatal(err)
	}
	return p, f
}
func TestProvisionEventsAndConfirmation(t *testing.T) {
	p, f := provisionFixture(t)
	ctx := context.Background()
	f.beforeSubscribe = func() {
		id, err := p.EventBrand(ctx, f.topic)
		if err != nil || id != 1 {
			t.Error("callback routing must be saved before subscribing", id, err)
		}
	}
	out, err := p.ProvisionEvents(ctx, 1, "https://postbox.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Configured || out.SubscriptionStatus != "pending_confirmation" || !strings.HasPrefix(out.ConfigurationSet, "postbox-") || out.TopicARN == "" {
		t.Fatal(out)
	}
	firstPolicy := f.policy
	var policy struct {
		Id        string
		Statement []struct {
			Sid       string
			Condition struct{ StringEquals map[string]string }
		}
	}
	if err = json.Unmarshal([]byte(f.policy), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Id != "existing" || len(policy.Statement) != 2 || policy.Statement[0].Sid != "OwnerAccess" || policy.Statement[1].Condition.StringEquals["AWS:SourceArn"] != "arn:aws:ses:eu-west-1:123456789012:configuration-set/"+out.ConfigurationSet {
		t.Fatal(f.policy)
	}
	saved, err := p.Settings(ctx, 1)
	if err != nil || saved.TopicARN != out.TopicARN {
		t.Fatal(saved, err)
	}
	again, err := p.ProvisionEvents(ctx, 1, "https://postbox.example.org/")
	if err != nil || again.ConfigurationSet != out.ConfigurationSet || f.policy != firstPolicy || f.calls["CreateTopic"] != 1 || f.calls["POST /v2/email/configuration-sets"] != 1 || f.calls["Subscribe"] != 2 {
		t.Fatal(again, err, f.calls)
	}
	f.subscription = f.topic + ":subscription-id"
	confirmed, err := p.ProvisionEvents(ctx, 1, "https://postbox.example.org")
	if err != nil || confirmed.SubscriptionStatus != "confirmed" || f.calls["Subscribe"] != 2 || f.calls["SetSubscriptionAttributes"] != 2 {
		t.Fatal(confirmed, err, f.calls)
	}
	mutations := f.calls["SetTopicAttributes"] + f.calls["Subscribe"] + f.calls["SetSubscriptionAttributes"]
	status, err := p.EventStatus(ctx, 1, "https://postbox.example.org")
	if err != nil || !status.Configured || status.SubscriptionStatus != "confirmed" || status.CheckedAt == 0 {
		t.Fatal(status, err)
	}
	if f.calls["SetTopicAttributes"]+f.calls["Subscribe"]+f.calls["SetSubscriptionAttributes"] != mutations {
		t.Fatal("status wrote to AWS")
	}
}
func TestProvisionEventsRecoversPartialFailure(t *testing.T) {
	p, f := provisionFixture(t)
	f.deny = "SetTopicAttributes"
	_, err := p.ProvisionEvents(context.Background(), 1, "https://postbox.example.org")
	if err == nil || !strings.Contains(err.Error(), "sns:SetTopicAttributes") || strings.Contains(err.Error(), "PRIVATE-SECRET") {
		t.Fatal(err)
	}
	f.deny = ""
	out, err := p.ProvisionEvents(context.Background(), 1, "https://postbox.example.org")
	if err != nil || !out.Configured || f.calls["CreateTopic"] != 1 {
		t.Fatal(out, err, f.calls)
	}
}
func TestProvisionEventsRejectsStaleSettings(t *testing.T) {
	p, f := provisionFixture(t)
	f.duringPolicy = func() {
		if _, err := p.db.Writer.Exec("UPDATE brands SET configuration_set='replacement' WHERE id=1"); err != nil {
			t.Error(err)
		}
	}
	_, err := p.ProvisionEvents(context.Background(), 1, "https://postbox.example.org")
	safe, ok := err.(*EventSetupError)
	if !ok || !safe.Conflict {
		t.Fatal(err)
	}
	var current string
	_ = p.db.Reader.QueryRow("SELECT configuration_set FROM brands WHERE id=1").Scan(&current)
	if current != "replacement" {
		t.Fatal("overwrote concurrent editor")
	}
}
func TestProvisionEventsUsesExistingResources(t *testing.T) {
	p, f := provisionFixture(t)
	f.topic = "arn:aws:sns:eu-west-1:123456789012:existing-events"
	f.config = true
	if err := p.Save(context.Background(), 1, Update{Mode: "static", Region: "eu-west-1", TopicARN: &f.topic}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Writer.Exec("UPDATE brands SET configuration_set='custom' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	out, err := p.ProvisionEvents(context.Background(), 1, "https://postbox.example.org")
	if err != nil || out.ConfigurationSet != "custom" || f.calls["CreateTopic"] != 0 || f.calls["POST /v2/email/configuration-sets"] != 0 {
		t.Fatal(out, err, f.calls)
	}
}
func TestEventEndpointValidation(t *testing.T) {
	for _, base := range []string{"", "http://postbox.example.org", "https://localhost", "https://127.0.0.1", "https://10.0.0.1", "https://user:pass@postbox.example.org", "https://postbox.example.org/path", "https://postbox.example.org?callback=elsewhere", "https://postbox.example.org#fragment"} {
		if _, err := eventEndpoint(base); err == nil {
			t.Errorf("accepted %q", base)
		}
	}
}
func TestTopicPolicyPreservation(t *testing.T) {
	for _, bad := range []string{"garbage", "null", `{"Statement":null}`, `{"Statement":[null]}`, `{"Statement":"oops"}`} {
		if _, err := eventTopicPolicy(bad, "topic", "source", "account", "owner"); err == nil {
			t.Errorf("accepted malformed policy %s", bad)
		}
	}
	first, err := eventTopicPolicy(`{"Statement":{"Sid":"existing","Effect":"Deny"}}`, "topic", "old-source", "account", "owner")
	if err != nil {
		t.Fatal(err)
	}
	second, err := eventTopicPolicy(first, "topic", "new-source", "account", "owner")
	if err != nil || strings.Contains(second, "old-source") || !strings.Contains(second, "existing") || !strings.Contains(second, "new-source") {
		t.Fatal(second, err)
	}
}

func TestProvisionRejectsInvalidTopicWithoutChangingAWS(t *testing.T) {
	for _, topic := range []string{"arn:aws:sns:eu-west-1:999999999999:other-account", "arn:aws:sns:eu-west-1:123456789012:events.fifo", "arn:aws:sns:us-east-1:123456789012:wrong-region"} {
		t.Run(topic, func(t *testing.T) {
			p, f := provisionFixture(t)
			if _, err := p.db.Writer.Exec("UPDATE brand_aws_settings SET sns_topic_arn=?,revision=revision+1 WHERE brand_id=1", topic); err != nil {
				t.Fatal(err)
			}
			_, err := p.ProvisionEvents(context.Background(), 1, "https://postbox.example.org")
			safe, ok := err.(*EventSetupError)
			if !ok || !safe.Invalid || f.calls["CreateTopic"] != 0 || f.calls["SetTopicAttributes"] != 0 || f.calls["Subscribe"] != 0 {
				t.Fatal(err, f.calls)
			}
		})
	}
}

func TestTopicARNPartitionMatchesRegion(t *testing.T) {
	for _, pair := range []struct{ region, partition string }{{"eu-west-1", "aws"}, {"us-gov-west-1", "aws-us-gov"}, {"cn-north-1", "aws-cn"}} {
		if !validTopicARN("arn:"+pair.partition+":sns:"+pair.region+":123456789012:events", pair.region) {
			t.Error("valid partition rejected", pair)
		}
		if validTopicARN("arn:wrong:sns:"+pair.region+":123456789012:events", pair.region) {
			t.Error("incorrect partition accepted", pair)
		}
	}
}
