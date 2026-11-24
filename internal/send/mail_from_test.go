package send

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

func mailFromTestSender(t *testing.T, handler http.Handler) *AWS {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AWS{Client: sesv2.NewFromConfig(aws.Config{
		Region: "eu-west-1", Credentials: credentials.NewStaticCredentialsProvider("fake", "fake", ""), HTTPClient: server.Client(),
	}, func(o *sesv2.Options) { o.BaseEndpoint = aws.String(server.URL) })}
}

func TestSESSenderIdentityPrecedence(t *testing.T) {
	const address = "Sender@news.example.org"
	const emailIdentity = `{"VerifiedForSendingStatus":true,"VerificationStatus":"SUCCESS","MailFromAttributes":{"MailFromDomain":"bounce.news.example.org"}}`
	const domainIdentity = `{"VerifiedForSendingStatus":true,"VerificationStatus":"SUCCESS","MailFromAttributes":{"MailFromDomain":"bounce.example.org"}}`
	const unverifiedIdentity = `{"VerifiedForSendingStatus":false,"VerificationStatus":"PENDING"}`
	for _, tc := range []struct {
		name, emailResponse, identity, domain string
		wantLookups                           []string
	}{
		{"verified email overrides domain", emailIdentity, address, "bounce.news.example.org", []string{address}},
		{"verified email uses SES default", `{"VerifiedForSendingStatus":true}`, address, "", []string{address}},
		{"domain fallback for absent email", "", "example.org", "bounce.example.org", []string{address, "news.example.org", "example.org"}},
		{"domain fallback for unverified email", unverifiedIdentity, "example.org", "bounce.example.org", []string{address, "news.example.org", "example.org"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups := make(chan string, 8)
			sender := mailFromTestSender(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimPrefix(r.URL.Path, "/v2/email/identities/")
				lookups <- name
				w.Header().Set("Content-Type", "application/json")
				if name == address && tc.emailResponse != "" {
					_, _ = w.Write([]byte(tc.emailResponse))
					return
				}
				if name == "example.org" {
					_, _ = w.Write([]byte(domainIdentity))
					return
				}
				w.Header().Set("X-Amzn-Errortype", "NotFoundException")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"identity not found"}`))
			}))
			name, attributes, err := sender.senderIdentity(t.Context(), address)
			var domain string
			if attributes != nil {
				domain = aws.ToString(attributes.MailFromDomain)
			}
			if err != nil || name != tc.identity || domain != tc.domain {
				t.Fatalf("identity=%q MAIL FROM=%q err=%v", name, domain, err)
			}
			var got []string
			for len(lookups) > 0 {
				got = append(got, <-lookups)
			}
			if !slices.Equal(got, tc.wantLookups) {
				t.Fatalf("lookups=%v want %v", got, tc.wantLookups)
			}
		})
	}
}

type mailFromTestResolver struct {
	txt []string
	mx  []*net.MX
}

func (r mailFromTestResolver) LookupTXT(context.Context, string) ([]string, error) { return r.txt, nil }
func (r mailFromTestResolver) LookupMX(context.Context, string) ([]*net.MX, error) { return r.mx, nil }

func TestMailFromDNS(t *testing.T) {
	const host = "feedback-smtp.eu-west-1.amazonses.com"
	validTXT := []string{"unrelated verification token", "v=SPF1 +include:AmazonSES.com -all"}
	validMX := []*net.MX{{Host: "FEEDBACK-SMTP.EU-WEST-1.AMAZONSES.COM.", Pref: 10}}
	for _, tc := range []struct {
		name string
		txt  []string
		mx   []*net.MX
		want []string
	}{
		{"valid records", validTXT, validMX, nil},
		{"both records missing", nil, nil, []string{"mail_from_spf", "mail_from_mx"}},
		{"wrong SPF include", []string{"v=spf1 include:amazonses.com.test ~all"}, validMX, []string{"mail_from_spf"}},
		{"negated first match", []string{"v=spf1 -include:amazonses.com include:amazonses.com ~all"}, validMX, []string{"mail_from_spf"}},
		{"duplicate SPF policies", []string{"v=spf1 include:amazonses.com ~all", "v=spf1 -all"}, validMX, []string{"mail_from_spf"}},
		{"wrong MX region", validTXT, []*net.MX{{Host: "feedback-smtp.us-east-1.amazonses.com.", Pref: 10}}, []string{"mail_from_mx"}},
		{"wrong MX priority", validTXT, []*net.MX{{Host: host, Pref: 20}}, []string{"mail_from_mx"}},
		{"multiple MX records", validTXT, []*net.MX{validMX[0], {Host: "mail.example.org.", Pref: 20}}, []string{"mail_from_mx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := checkMailFromDNS(t.Context(), mailFromTestResolver{tc.txt, tc.mx}, "sender@example.org", "bounce.example.org", host)
			var codes []string
			for _, issue := range issues {
				codes = append(codes, issue.Code)
			}
			if !slices.Equal(codes, tc.want) {
				t.Fatalf("issues=%+v want codes %v", issues, tc.want)
			}
		})
	}
}

func TestChecklistAndDispatchBlockInvalidPreparedSenderMailFrom(t *testing.T) {
	db, e, fake := fixture(t, 1)
	ctx := t.Context()
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html='<p>Hello [Name]</p>' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.prepare(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE brands SET from_email='new-sender@example.org' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", fake.Handler())
	mux.HandleFunc("GET /v2/email/identities/sender@example.org", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The invalid DNS name causes both lookups to fail without network access.
		_, _ = w.Write([]byte(`{"VerifiedForSendingStatus":true,"VerificationStatus":"SUCCESS","MailFromAttributes":{"MailFromDomain":"broken domain.example.org","MailFromDomainStatus":"SUCCESS"}}`))
	})
	e.sender = mailFromTestSender(t, mux)
	checks, err := e.Checklist(ctx, 1, PreflightOptions{})
	issues := checks.Checks.Errors
	if err != nil || checks.Checks.Ready || len(issues) != 2 || issues[0].Code != "mail_from_spf" || issues[1].Code != "mail_from_mx" {
		t.Fatalf("ready=%t issues=%+v err=%v", checks.Checks.Ready, issues, err)
	}
	if err := e.CheckCampaignSender(ctx, 1); err == nil || !strings.Contains(err.Error(), "broken domain.example.org") {
		t.Fatalf("queue check error=%v", err)
	}
	if err := e.RunCampaign(ctx, 1); err == nil || !strings.Contains(err.Error(), "broken domain.example.org") {
		t.Fatalf("dispatch error=%v", err)
	}
	if calls := fake.Calls.Load(); calls != 0 {
		t.Fatalf("sent %d messages with invalid MAIL FROM DNS", calls)
	}
}
