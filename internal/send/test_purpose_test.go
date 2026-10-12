package send

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

type testPurposeSender struct {
	request Request
}

func (*testPurposeSender) GetAccount(context.Context) (Quota, error) {
	return Quota{10000, 100000, 0}, nil
}

func (s *testPurposeSender) Send(_ context.Context, r Request) (string, error) {
	s.request = r
	return "test-message", nil
}

func TestCampaignTestSendHasNoProductionCampaignIdentity(t *testing.T) {
	db, engine, _ := fixture(t, 1)
	sender := &testPurposeSender{}
	engine.sender = sender
	if err := engine.TestCampaign(context.Background(), 1, []string{"reader1@example.org"}, 1); err != nil {
		t.Fatal(err)
	}
	if sender.request.BrandID != 1 || sender.request.CampaignID != 0 || sender.request.Purpose != TestPurpose {
		t.Fatalf("test request carries production identity: brand=%d campaign=%d purpose=%q", sender.request.BrandID, sender.request.CampaignID, sender.request.Purpose)
	}
	var count int
	if err := db.Reader.QueryRow(`SELECT count(*) FROM send_attempts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("test send created production attempt: %d %v", count, err)
	}
}

func TestProductionDispatchCarriesReceiptCorrelation(t *testing.T) {
	db, engine, _ := fixture(t, 1)
	sender := &testPurposeSender{}
	engine.sender = sender
	if err := engine.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var campaign, subscriber string
	if err := db.Reader.QueryRow(`SELECT c.tracking_identity,s.tracking_identity FROM campaigns c JOIN subscribers s ON s.id=1 WHERE c.id=1`).Scan(&campaign, &subscriber); err != nil {
		t.Fatal(err)
	}
	r := sender.request
	if r.CampaignIdentity != campaign || r.SubscriberIdentity != subscriber || r.Attempt != 1 {
		t.Fatalf("missing dispatch correlation: campaign=%q subscriber=%q attempt=%d", r.CampaignIdentity, r.SubscriberIdentity, r.Attempt)
	}
}

func TestAWSTagsDistinguishTestTransactionalAndCampaignMail(t *testing.T) {
	for _, tc := range []struct {
		name       string
		campaign   int64
		purpose    string
		wantTagged bool
	}{
		{"campaign", 7, "", true},
		{"test", 7, TestPurpose, false},
		{"transactional", 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tagRequests := make(chan map[string]string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					EmailTags []struct{ Name, Value string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				tags := make(map[string]string)
				for _, tag := range body.EmailTags {
					tags[tag.Name] = tag.Value
				}
				tagRequests <- tags
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"MessageId":"test-message"}`))
			}))
			defer server.Close()
			client := sesv2.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""), HTTPClient: server.Client()}, func(o *sesv2.Options) { o.BaseEndpoint = aws.String(server.URL) })
			if _, err := (&AWS{client}).Send(context.Background(), Request{BrandID: 1, CampaignID: tc.campaign, CampaignIdentity: "campaign-nonce", SubscriberIdentity: "subscriber-nonce", Attempt: 3, Purpose: tc.purpose, From: "sender@example.org", To: "reader@example.org", Raw: []byte("Subject: Test\r\n\r\nHello")}); err != nil {
				t.Fatal(err)
			}
			tags := <-tagRequests
			if tags["brand_id"] != "1" {
				t.Fatalf("missing brand scope: %v", tags)
			}
			_, tagged := tags["campaign_id"]
			if tagged != tc.wantTagged || tags["message_purpose"] != tc.purpose {
				t.Fatalf("unexpected dispatch tags: %v", tags)
			}
			for key, value := range map[string]string{"campaign_identity": "campaign-nonce", "subscriber_identity": "subscriber-nonce", "send_attempt": "3"} {
				want := ""
				if tc.wantTagged {
					want = value
				}
				if tags[key] != want {
					t.Fatalf("incorrect %s tag: %v", key, tags)
				}
			}
		})
	}
}
