package awsbrand

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"postbox/internal/send"
	"postbox/internal/store"
)

func TestStatusWithDevelopmentAWSService(t *testing.T) {
	fake := &send.Fake{Config: send.FakeConfig{Rate: 175}}
	server := httptest.NewServer(fake.Handler())
	defer server.Close()
	p, _ := testProvider(t, server.URL)
	ctx := context.Background()
	if err := p.Save(ctx, 1, Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "fake-key", SecretAccessKey: "fake-secret"}); err != nil {
		t.Fatal(err)
	}
	status, err := p.Status(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if status.AccountID != "123456789012" || status.Region != "eu-west-1" || status.Quota.MaxSendRate != 175 || !status.SendingEnabled || !status.ProductionAccessEnabled || !status.CustomEndpoint {
		t.Fatalf("unexpected development status: %+v", status)
	}
}

func TestBrandEventTopicUsesSavedSettings(t *testing.T) {
	p, _ := testProvider(t, "")
	ctx := context.Background()
	topic := "arn:aws:sns:eu-west-1:123456789012:brand-one"
	if err := p.Save(ctx, 1, Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "key", SecretAccessKey: "secret", TopicARN: &topic}); err != nil {
		t.Fatal(err)
	}
	if id, err := p.EventBrand(ctx, topic); err != nil || id != 1 {
		t.Fatalf("topic resolved to %d: %v", id, err)
	}
	if err := p.Save(ctx, 2, Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "other", SecretAccessKey: "other", TopicARN: &topic}); err == nil {
		t.Fatal("SNS topic assigned to two brands")
	}
	blank := ""
	if err := p.Save(ctx, 1, Update{Mode: "static", Region: "eu-west-1", TopicARN: &blank}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EventBrand(ctx, topic); err == nil {
		t.Fatal("cleared topic still routed")
	}
}

func TestSetupEventsUsesBrandKeys(t *testing.T) {
	for _, initial := range []string{"missing", "existing", "destination", "denied"} {
		t.Run(initial, func(t *testing.T) {
			configExists := initial != "missing"
			destinationExists := initial == "destination"
			var createdConfig, configured bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=brand-event-key/") {
					t.Error("event setup did not use brand keys")
				}
				if r.URL.Path == "/" {
					w.Header().Set("Content-Type", "text/xml")
					fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v2/email/configuration-sets" && r.Method == http.MethodPost {
					if configExists {
						t.Error("attempted to create an existing configuration set")
					}
					configExists, createdConfig = true, true
					fmt.Fprint(w, `{}`)
					return
				}
				if r.URL.Path == "/v2/email/configuration-sets/postbox/event-destinations" && r.Method == http.MethodGet {
					if initial == "denied" {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `{"__type":"AccessDeniedException","message":"denied"}`)
					} else if !configExists {
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"__type":"NotFoundException","message":"missing"}`)
					} else if destinationExists {
						fmt.Fprint(w, `{"EventDestinations":[{"Name":"postbox"}]}`)
					} else {
						fmt.Fprint(w, `{"EventDestinations":[]}`)
					}
					return
				}
				if (r.URL.Path == "/v2/email/configuration-sets/postbox/event-destinations" && r.Method == http.MethodPost && !destinationExists) || (r.URL.Path == "/v2/email/configuration-sets/postbox/event-destinations/postbox" && r.Method == http.MethodPut && destinationExists) {
					var body struct {
						EventDestinationName string
						EventDestination     struct {
							Enabled            bool
							MatchingEventTypes []string
							SnsDestination     struct{ TopicArn string }
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if !body.EventDestination.Enabled || body.EventDestination.SnsDestination.TopicArn != "arn:aws:sns:eu-west-1:123456789012:brand-one" || len(body.EventDestination.MatchingEventTypes) != 7 {
						t.Errorf("unexpected event destination: %+v", body)
					}
					configured = true
					fmt.Fprint(w, `{}`)
					return
				}
				t.Errorf("unexpected AWS request: %s %s", r.Method, r.URL.Path)
				http.Error(w, "unexpected request", 404)
			}))
			defer server.Close()
			p, db := testProvider(t, server.URL)
			if _, err := db.Writer.Exec("UPDATE brands SET configuration_set='postbox' WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			topic := "arn:aws:sns:eu-west-1:123456789012:brand-one"
			if err := p.Save(context.Background(), 1, Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "brand-event-key", SecretAccessKey: "secret", TopicARN: &topic}); err != nil {
				t.Fatal(err)
			}
			err := p.SetupEvents(context.Background(), 1)
			if initial == "denied" {
				if err == nil || createdConfig || configured {
					t.Fatalf("permission failure triggered setup: %v", err)
				}
				return
			}
			if err != nil || !configured || createdConfig != (initial == "missing") {
				t.Fatalf("configuration setup: created=%v configured=%v err=%v", createdConfig, configured, err)
			}
		})
	}
}

func testProvider(t *testing.T, endpoint string) (*Provider, *store.DB) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	_, e = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'One','One','one@example.test',1),(2,'Two','Two','two@example.test',1)`)
	if e != nil {
		t.Fatal(e)
	}
	p, e := New(db, []byte(strings.Repeat("s", 32)), "us-east-1", endpoint, 8)
	if e != nil {
		t.Fatal(e)
	}
	return p, db
}
func TestEncryptedCredentialsAndMetadata(t *testing.T) {
	ctx := context.Background()
	p, db := testProvider(t, "")
	u := Update{Mode: "static", Region: "us-east-1", AccessKeyID: "ACCESS_UNIQUE", SecretAccessKey: "SECRET_UNIQUE", SessionToken: "TOKEN_UNIQUE"}
	if e := p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	var b []byte
	if e := db.Reader.QueryRow("SELECT credentials FROM brand_aws_settings WHERE brand_id=1").Scan(&b); e != nil {
		t.Fatal(e)
	}
	s, e := p.Settings(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(s)
	for _, secret := range []string{u.AccessKeyID, u.SecretAccessKey, u.SessionToken} {
		if strings.Contains(string(b), secret) || strings.Contains(string(encoded), secret) {
			t.Fatal("credential was exposed")
		}
	}
	if !s.HasCredentials || !s.HasSessionToken {
		t.Fatal("presence flags missing")
	}
	if _, e := p.open(2, b); e == nil {
		t.Fatal("ciphertext must be bound to its brand")
	}
	other, e := New(db, []byte(strings.Repeat("x", 32)), "us-east-1", "", 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.open(1, b); e == nil {
		t.Fatal("different server key decrypted credentials")
	}
	if e = p.Save(ctx, 1, Update{Mode: "static", Region: "eu-west-1"}); e != nil {
		t.Fatal(e)
	}
	r, e := read(ctx, db.Reader, 1)
	if e != nil {
		t.Fatal(e)
	}
	m, e := p.open(1, r.encrypted)
	if e != nil {
		t.Fatal(e)
	}
	if m.SecretAccessKey != u.SecretAccessKey || m.SessionToken != u.SessionToken {
		t.Fatal("blank update did not preserve secrets")
	}
	if e = p.Save(ctx, 1, Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "NEW_ACCESS"}); e == nil {
		t.Fatal("access key rotated without matching secret")
	}
	if e = p.Save(ctx, 1, Update{Mode: "instance_role", Region: "eu-west-1"}); e == nil {
		t.Fatal("server credentials mode accepted")
	}
}
func TestClientCacheBrandIsolationRotationAndQuotaScope(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account><Arn>arn:aws:iam::123456789012:user/test</Arn><UserId>test</UserId></GetCallerIdentityResult><ResponseMetadata><RequestId>test</RequestId></ResponseMetadata></GetCallerIdentityResponse>`)
	}))
	defer server.Close()
	p, _ := testProvider(t, server.URL)
	ctx := context.Background()
	u := Update{Mode: "static", Region: "us-east-1", AccessKeyID: "test-access", SecretAccessKey: "test-secret"}
	for _, id := range []int64{1, 2} {
		if e := p.Save(ctx, id, u); e != nil {
			t.Fatal(e)
		}
	}
	a, e := p.ResolveBrand(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	b, e := p.ResolveBrand(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	again, e := p.ResolveBrand(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if a.Sender == b.Sender {
		t.Fatal("brands shared a client")
	}
	if a.Sender != again.Sender || calls.Load() != 2 {
		t.Fatal("client cache missed")
	}
	if a.Scope != "123456789012:us-east-1" || a.Scope != b.Scope {
		t.Fatal("account-region quota scope mismatch")
	}
	u.SecretAccessKey = "rotated-secret"
	if e := p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	rotated, e := p.ResolveBrand(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if rotated.Sender == a.Sender {
		t.Fatal("credential rotation reused client")
	}
	other, e := p.ResolveBrand(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	if other.Sender != b.Sender {
		t.Fatal("rotation invalidated another brand")
	}
	u.Region = "eu-west-1"
	if e := p.Save(ctx, 2, u); e != nil {
		t.Fatal(e)
	}
	other, e = p.ResolveBrand(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	if other.Scope == a.Scope {
		t.Fatal("different regions share quota")
	}
}
func TestInvalidConfigurationNeverResolvesToFallback(t *testing.T) {
	p, db := testProvider(t, "")
	ctx := context.Background()
	t.Setenv("AWS_ACCESS_KEY_ID", "server-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "server-secret")
	if _, e := p.ResolveBrand(ctx, 1); e == nil || e.Error() != "brand AWS access keys are not configured" {
		t.Fatalf("unconfigured brand used server credentials: %v", e)
	}
	if e := p.Save(ctx, 1, Update{Mode: "static", Region: "us-east-1"}); e == nil {
		t.Fatal("missing static credentials accepted")
	}
	if e := p.Save(ctx, 1, Update{Mode: "static"}); e == nil {
		t.Fatal("missing region accepted")
	}
	_, e := db.Writer.Exec(`INSERT INTO brand_aws_settings(brand_id,mode,region,credentials,updated_at) VALUES(1,'static','us-east-1',x'00',1)`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.ResolveBrand(ctx, 1); e == nil || strings.Contains(e.Error(), "00") {
		t.Fatal("corrupt static credentials must fail safely")
	}
}

func TestMigrationRemovesServerCredentialConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'One','One','one@example.test',1),(2,'Two','Two','two@example.test',1);
		DROP TABLE brand_aws_settings;
		CREATE TABLE brand_aws_settings (brand_id INTEGER PRIMARY KEY REFERENCES brands(id) ON DELETE CASCADE, mode TEXT NOT NULL CHECK(mode IN ('static','instance_role')), region TEXT NOT NULL, credentials BLOB NOT NULL, revision INTEGER NOT NULL DEFAULT 1, updated_at INTEGER NOT NULL) STRICT;
		INSERT INTO brand_aws_settings(brand_id,mode,region,credentials,updated_at) VALUES(1,'instance_role','us-east-1',x'00',1),(2,'static','eu-west-1',x'01',1);
		INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(1,'email','old@example.test','ses_sync',1),(2,'email','keep@example.test','ses_sync',1);
		DELETE FROM schema_migrations WHERE name IN ('012_brand_keys_only.sql','013_brand_ses_events.sql')`)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	if e = db.Reader.QueryRow("SELECT count(*) FROM brand_aws_settings WHERE mode='instance_role'").Scan(&count); e != nil || count != 0 {
		t.Fatalf("legacy server connection retained: %d %v", count, e)
	}
	if e = db.Reader.QueryRow("SELECT count(*) FROM brand_aws_settings WHERE brand_id=2 AND mode='static'").Scan(&count); e != nil || count != 1 {
		t.Fatalf("brand keys removed: %d %v", count, e)
	}
	if e = db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id=1 AND reason='ses_sync'").Scan(&count); e != nil || count != 0 {
		t.Fatalf("legacy imported suppression retained: %d %v", count, e)
	}
	if _, e = db.Writer.Exec("INSERT INTO brand_aws_settings(brand_id,mode,region,credentials,updated_at) VALUES(1,'instance_role','us-east-1',x'00',1)"); e == nil {
		t.Fatal("schema still accepts server credential mode")
	}
}

func TestResolutionDisablesRetriesAndUsesStaticCredentials(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=isolated-access/") {
			t.Error("request did not use brand credentials")
		}
		if r.Header.Get("X-Amz-Security-Token") != "isolated-token" {
			t.Error("request did not use brand session token")
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `<ErrorResponse><Error><Type>Receiver</Type><Code>ServiceUnavailable</Code><Message>isolated-secret isolated-token</Message></Error><RequestId>test</RequestId></ErrorResponse>`)
	}))
	defer server.Close()
	p, _ := testProvider(t, server.URL)
	ctx := context.Background()
	if e := p.Save(ctx, 1, Update{Mode: "static", Region: "us-east-1", AccessKeyID: "isolated-access", SecretAccessKey: "isolated-secret", SessionToken: "isolated-token"}); e != nil {
		t.Fatal(e)
	}
	_, e := p.ResolveBrand(ctx, 1)
	if e == nil {
		t.Fatal("failed AWS account lookup succeeded")
	}
	if calls.Load() != 1 {
		t.Fatalf("resolution retried %d times", calls.Load())
	}
	if strings.Contains(e.Error(), "isolated-") {
		t.Fatal("AWS error leaked credential material")
	}
	if p.http.Timeout.Seconds() != 30 {
		t.Fatal("AWS HTTP timeout is not 30 seconds")
	}
}

func TestConnectionChangesResetOnlyImportedSuppressions(t *testing.T) {
	p, db := testProvider(t, "")
	ctx := context.Background()
	u := Update{Mode: "static", Region: "us-east-1", AccessKeyID: "access", SecretAccessKey: "secret", SessionToken: "token"}
	if e := p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	seed := func() {
		t.Helper()
		_, e := db.Writer.Exec(`DELETE FROM jobs; DELETE FROM suppressions; INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(1,'email','old@example.test','ses_sync',1),(2,'email','other@example.test','ses_sync',1),(1,'email','manual@example.test','manual',1),(1,'email','bounce@example.test','hard_bounce',1),(NULL,'domain','manual.test','manual',1); INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('ses_suppressions','{}','pending',9999999999999,1)`)
		if e != nil {
			t.Fatal(e)
		}
	}
	assertReset := func() {
		t.Helper()
		var count int
		if e := db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id=1 AND reason='ses_sync'").Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count != 0 {
			t.Fatal("previous account import retained")
		}
		if e := db.Reader.QueryRow("SELECT count(*) FROM suppressions").Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count != 4 {
			t.Fatal("unrelated suppression removed")
		}
		var at int64
		if e := db.Reader.QueryRow("SELECT run_at FROM jobs WHERE queue='ses_suppressions' AND status='pending'").Scan(&at); e != nil {
			t.Fatal(e)
		}
		if at > time.Now().UnixMilli() {
			t.Fatal("suppression refresh was not advanced")
		}
	}
	seed()
	if e := p.Save(ctx, 1, Update{Mode: "static", Region: "us-east-1"}); e != nil {
		t.Fatal(e)
	}
	var count int
	db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id=1 AND reason='ses_sync'").Scan(&count)
	if count != 1 {
		t.Fatal("unchanged blank update cleared imports")
	}
	var at int64
	db.Reader.QueryRow("SELECT run_at FROM jobs WHERE queue='ses_suppressions'").Scan(&at)
	if at != 9999999999999 {
		t.Fatal("unchanged update rescheduled imports")
	}
	// Writing the same credential values must also preserve the existing imports.
	if e := p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id=1 AND reason='ses_sync'").Scan(&count)
	if count != 1 {
		t.Fatal("same credential values cleared imports")
	}
	for _, change := range []Update{{Mode: "static", Region: "us-east-1", SecretAccessKey: "new-secret"}, {Mode: "static", Region: "eu-west-1"}, {Mode: "static", Region: "eu-west-1", ClearSessionToken: true}} {
		seed()
		if e := p.Save(ctx, 1, change); e != nil {
			t.Fatal(e)
		}
		assertReset()
	}
	seed()
	if e := p.Delete(ctx, 1); e != nil {
		t.Fatal(e)
	}
	assertReset()
	// Reconfiguring a deleted brand clears old imported suppressions.
	seed()
	if e := p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	assertReset()
	// Schedule a refresh even when there is no existing pending job.
	db.Writer.Exec("DELETE FROM jobs")
	if e := p.Delete(ctx, 1); e != nil {
		t.Fatal(e)
	}
	db.Reader.QueryRow("SELECT count(*) FROM jobs WHERE queue='ses_suppressions' AND status='pending'").Scan(&count)
	if count != 1 {
		t.Fatal("reset did not enqueue missing refresh")
	}
}
func TestMigrationScopesLegacySESImports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'One','One','one@example.test',1),(2,'Two','Two','two@example.test',1); INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(NULL,'email','legacy@example.test','ses_sync',1),(NULL,'domain','blocked.test','manual',1),(1,'email','legacy@example.test','manual',1); DROP TABLE brand_aws_settings; DELETE FROM schema_migrations WHERE name='010_brand_aws.sql'`)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id IS NULL AND reason='ses_sync'").Scan(&count)
	if count != 0 {
		t.Fatal("legacy account suppression remained global")
	}
	db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id=2 AND reason='ses_sync' AND value='legacy@example.test'").Scan(&count)
	if count != 1 {
		t.Fatal("legacy import not preserved for existing brand")
	}
	db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE reason='manual'").Scan(&count)
	if count != 2 {
		t.Fatal("manual suppression altered during migration")
	}
	_, e = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(3,'Three','Three','three@example.test',1)`)
	if e != nil {
		t.Fatal(e)
	}
	db.Reader.QueryRow("SELECT count(*) FROM suppressions WHERE brand_id=3").Scan(&count)
	if count != 0 {
		t.Fatal("new brand inherited old account imports")
	}
}

func TestSnapshotGuardRejectsRotationAndDeleteRecreate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
	}))
	defer server.Close()
	p, db := testProvider(t, server.URL)
	ctx := context.Background()
	u := Update{Mode: "static", Region: "us-east-1", AccessKeyID: "access", SecretAccessKey: "secret"}
	if e := p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	first, snapshot, e := p.SESClientSnapshot(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if first == nil || snapshot == "" {
		t.Fatal("snapshot missing client or guard")
	}
	check := func(s string) error {
		return db.Immediate(ctx, func(c *sql.Conn) error { return p.CheckSnapshot(ctx, c, 1, s) })
	}
	if e = check(snapshot); e != nil {
		t.Fatal(e)
	}
	u.SecretAccessKey = "rotated"
	if e = p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	if e = check(snapshot); e == nil {
		t.Fatal("old client snapshot accepted following rotation")
	}
	_, rotated, e := p.SESClientSnapshot(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = check(rotated); e != nil {
		t.Fatal(e)
	}
	if e = p.Delete(ctx, 1); e != nil {
		t.Fatal(e)
	}
	if e = check(rotated); e == nil {
		t.Fatal("static snapshot accepted after reset")
	}
	if e = p.Save(ctx, 1, u); e != nil {
		t.Fatal(e)
	}
	_, recreated, e := p.SESClientSnapshot(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if recreated == rotated || check(rotated) == nil {
		t.Fatal("delete/recreate reused previous credential fingerprint")
	}
}

func TestStatusMissingQuotaFailsExplicitly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/email/account" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"SendingEnabled":true}`)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
	}))
	defer server.Close()
	p, _ := testProvider(t, server.URL)
	ctx := context.Background()
	if e := p.Save(ctx, 1, Update{Mode: "static", Region: "us-east-1", AccessKeyID: "access", SecretAccessKey: "secret"}); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Status(ctx, 1); e == nil || e.Error() != "SES returned no sending quota" {
		t.Fatalf("missing quota status: %v", e)
	}
}
