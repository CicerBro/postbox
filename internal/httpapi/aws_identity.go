package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"postbox/internal/awsbrand"
)

type dnsRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
}

type sesIdentityResponse struct {
	awsbrand.Identity
	CheckedAt  int64       `json:"checked_at"`
	DNSRecords []dnsRecord `json:"dns_records"`
}

// Domain setup only accepts DNS names, never addresses, URLs, or identity ARNs.
func sesDomain(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	domain, err := idna.Lookup.ToASCII(value)
	domain = strings.ToLower(domain)
	if err != nil || len(domain) > 253 || net.ParseIP(domain) != nil || !strings.Contains(domain, ".") {
		return "", errors.New("enter a valid domain name, such as example.com")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("enter a valid domain name, such as example.com")
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return "", errors.New("enter a valid domain name, such as example.com")
			}
		}
	}
	return domain, nil
}

func identityDNS(out awsbrand.Identity) []dnsRecord {
	records := []dnsRecord{}
	// BYODKIM requires a public key; DEED replicas require no extra records.
	// Never guess the signing zone, which can vary by region and identity.
	if out.IdentityType == "DOMAIN" && out.DKIMOrigin == "AWS_SES" && out.DKIMHostedZone != "" {
		for _, token := range out.DKIMTokens {
			records = append(records, dnsRecord{Type: "CNAME", Name: token + "._domainkey." + out.Identity, Value: token + "." + strings.TrimSuffix(out.DKIMHostedZone, ".")})
		}
	}
	if out.MailFromDomain != "" && out.Region != "" {
		records = append(records,
			dnsRecord{Type: "MX", Name: out.MailFromDomain, Value: "feedback-smtp." + out.Region + ".amazonses.com", Priority: 10},
			dnsRecord{Type: "TXT", Name: out.MailFromDomain, Value: "v=spf1 include:amazonses.com ~all"},
		)
	}
	return records
}

func decodeIdentityJSON(w http.ResponseWriter, r *http.Request, in any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var trailing any
	if d.Decode(in) != nil || d.Decode(&trailing) != io.EOF {
		fail(w, 400, errors.New("invalid SES identity JSON"))
		return false
	}
	return true
}

func (a *API) awsIdentity(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("user session required"))
		return
	}
	id, ok := a.awsScope(w, r, r.Method != http.MethodGet)
	if !ok {
		return
	}
	var in struct {
		Identity string `json:"identity"`
	}
	if r.Method == http.MethodGet {
		in.Identity = r.URL.Query().Get("identity")
	} else if !decodeIdentityJSON(w, r, &in) {
		return
	}
	name, err := sesDomain(in.Identity)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if !a.allow(fmt.Sprintf("aws-identity:%s:%d:%d", r.Method, i.UserID, id), 6) {
		fail(w, 429, errors.New("SES identity request rate limit exceeded"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out awsbrand.Identity
	if r.Method == http.MethodGet {
		out, err = a.cfg.AWSProvider.GetIdentity(ctx, id, name)
	} else {
		out, err = a.cfg.AWSProvider.VerifyIdentity(ctx, id, name)
	}
	if errors.Is(err, awsbrand.ErrIdentityNotFound) {
		fail(w, 404, errors.New("this domain has no SES identity in the brand's AWS account and region"))
		return
	}
	if err != nil {
		fail(w, 502, errors.New("SES identity request failed. Check the brand credentials, region, and SES identity permissions."))
		return
	}
	respond(w, 200, sesIdentityResponse{Identity: out, CheckedAt: time.Now().UnixMilli(), DNSRecords: identityDNS(out)})
}

func (a *API) awsMailFrom(w http.ResponseWriter, r *http.Request) {
	id, ok := a.awsScope(w, r, true)
	if !ok {
		return
	}
	var in struct {
		Identity       string  `json:"identity"`
		MailFromDomain *string `json:"mail_from_domain"`
		Behavior       string  `json:"mail_from_behavior"`
	}
	if !decodeIdentityJSON(w, r, &in) {
		return
	}
	name, err := sesDomain(in.Identity)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if in.MailFromDomain == nil || (in.Behavior != "USE_DEFAULT_VALUE" && in.Behavior != "REJECT_MESSAGE") {
		fail(w, 400, errors.New("mail_from_domain and a valid mail_from_behavior are required"))
		return
	}
	domain := strings.TrimSpace(*in.MailFromDomain)
	if domain != "" {
		domain, err = sesDomain(domain)
		if err != nil || !strings.HasSuffix(domain, "."+name) {
			fail(w, 400, errors.New("MAIL FROM must be a subdomain of the identity, such as bounce.example.com"))
			return
		}
	}
	i := r.Context().Value(identityKey{}).(*identity)
	if !a.allow(fmt.Sprintf("aws-mail-from:%d:%d", i.UserID, id), 6) {
		fail(w, 429, errors.New("MAIL FROM setup rate limit exceeded"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err = a.cfg.AWSProvider.SetMailFrom(ctx, id, name, domain, in.Behavior); err != nil {
		fail(w, 502, errors.New("SES MAIL FROM setup failed. Check identity verification and ses:PutEmailIdentityMailFromAttributes permission."))
		return
	}
	respond(w, 200, map[string]bool{"configured": domain != ""})
}
