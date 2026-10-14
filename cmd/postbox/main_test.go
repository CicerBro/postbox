package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"postbox/internal/send"
	"postbox/internal/store"
)

func TestCLIInitializationAndOperations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postbox.db")
	args := []string{"--db", path}
	if err := run(append([]string{"not-a-command"}, args...)); err == nil {
		t.Fatal("unknown command accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unknown command touched database")
	}
	if err := run(append([]string{"serve", "--help"}, args...)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("help touched database")
	}
	if err := run(append([]string{"init"}, args...)); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enabled := func(want string) {
		t.Helper()
		var got string
		if err := db.Reader.QueryRow("SELECT value FROM settings WHERE key='sending_enabled'").Scan(&got); err != nil || got != want {
			t.Fatalf("sending=%q want=%q err=%v", got, want, err)
		}
	}
	enabled("false")
	if _, err := db.Writer.Exec("UPDATE settings SET value='true' WHERE key='sending_enabled'"); err != nil {
		t.Fatal(err)
	}
	if err := run(append([]string{"init"}, args...)); err != nil {
		t.Fatal(err)
	}
	enabled("true")
	t.Setenv("POSTBOX_ADMIN_EMAIL", "admin@example.org")
	t.Setenv("POSTBOX_ADMIN_PASSWORD", "a long unique test password 123")
	t.Setenv("POSTBOX_ADMIN_TOTP", "JBSWY3DPEHPK3PXP")
	if err := run(append([]string{"admin"}, args...)); err != nil {
		t.Fatal(err)
	}
	var hash, secret string
	if err := db.Reader.QueryRow("SELECT password_hash,totp_secret FROM users WHERE email='admin@example.org'").Scan(&hash, &secret); err != nil {
		t.Fatal(err)
	}
	if hash == os.Getenv("POSTBOX_ADMIN_PASSWORD") || secret != os.Getenv("POSTBOX_ADMIN_TOTP") {
		t.Fatal("admin factors not persisted correctly")
	}
	backup := filepath.Join(dir, "backup.sqlite")
	t.Setenv("POSTBOX_BACKUP_FILE", backup)
	if err := run(append([]string{"backup"}, args...)); err != nil {
		t.Fatal(err)
	}
	if err := run(append([]string{"backup"}, args...)); err == nil {
		t.Fatal("backup overwrote existing file")
	}
	dump := filepath.Join(dir, "sendy.sql")
	if err := os.WriteFile(dump, []byte("INSERT INTO apps(id,app_name,from_email) VALUES(1,'Brand','sender@example.org'); INSERT INTO lists(id,app,name) VALUES(1,1,'Main'); INSERT INTO subscribers(id,list,email,status) VALUES(1,1,'member@example.org','active');"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POSTBOX_SENDY_SOURCE_ID", "cli-test")
	t.Setenv("POSTBOX_SENDY_DUMP", dump)
	t.Setenv("POSTBOX_SENDY_DRY_RUN", "true")
	if err := run(append([]string{"migrate-sendy"}, args...)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Reader.QueryRow("SELECT count(*) FROM subscribers").Scan(&count); err != nil || count != 0 {
		t.Fatalf("dry run committed: %d %v", count, err)
	}
	t.Setenv("POSTBOX_SENDY_DRY_RUN", "false")
	for range 2 {
		if err := run(append([]string{"migrate-sendy"}, args...)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Reader.QueryRow("SELECT count(*) FROM subscribers").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration not idempotent: %d %v", count, err)
	}
}

func TestFirstCLIAdoptionDisablesExistingDatabaseOnce(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, want := range []string{"false", "true"} {
		if err := bootstrapSending(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := db.Reader.QueryRow("SELECT value FROM settings WHERE key='sending_enabled'").Scan(&got); err != nil || got != want {
			t.Fatalf("got %s want %s err %v", got, want, err)
		}
		if _, err := db.Writer.Exec("UPDATE settings SET value='true' WHERE key='sending_enabled'"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAWSStandardProfileChainUsesRealSDKAgainstFakeEndpoint(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "credentials")
	if err := os.WriteFile(profile, []byte("[postbox-test]\naws_access_key_id = TEST_ONLY_KEY\naws_secret_access_key = TEST_ONLY_SECRET\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", profile)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "no-config"))
	t.Setenv("AWS_PROFILE", "postbox-test")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	var sent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=TEST_ONLY_KEY/") {
			t.Error("shared profile credentials not used")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/email/account":
			w.Write([]byte(`{"SendingEnabled":true,"SendQuota":{"MaxSendRate":175,"Max24HourSend":1000000,"SentLast24Hours":0}}`))
		case "/v2/email/outbound-emails":
			var body struct {
				Destination struct{ ToAddresses []string }
				Content     struct{ Raw struct{ Data string } }
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.Destination.ToAddresses) != 1 || body.Destination.ToAddresses[0] != "reader@example.org" || body.Content.Raw.Data == "" {
				t.Error("SDK omitted destination or MIME")
			}
			sent = true
			w.Write([]byte(`{"MessageId":"fake-accepted"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	sender, err := send.NewAWS(context.Background(), "eu-west-1", server.URL, 2)
	if err != nil {
		t.Fatal(err)
	}
	quota, err := sender.GetAccount(context.Background())
	if err != nil || quota.MaxSendRate != 175 {
		t.Fatalf("quota %+v %v", quota, err)
	}
	id, err := sender.Send(context.Background(), send.Request{From: "sender@example.org", To: "reader@example.org", Raw: []byte("Subject: Test\r\n\r\nTest"), BrandID: 1, CampaignID: 1})
	if err != nil || id != "fake-accepted" || !sent {
		t.Fatalf("send %s %v", id, err)
	}
}

// Run the real serve lifecycle in a child so SIGTERM cannot interrupt go test.
func TestServeProcess(t *testing.T) {
	if os.Getenv("POSTBOX_TEST_SERVE_CHILD") != "1" {
		return
	}
	if err := run([]string{"serve"}); err != nil {
		t.Fatal(err)
	}
}

func TestServeHealthAndGracefulShutdown(t *testing.T) {
	testServeShutdown(t, false)
}

func TestServeShutdownWithActiveReport(t *testing.T) {
	testServeShutdown(t, true)
}

func testServeShutdown(t *testing.T, activeReport bool) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/email/account" {
			w.Write([]byte(`{"SendingEnabled":true,"SendQuota":{"MaxSendRate":175,"Max24HourSend":1000000,"SentLast24Hours":0}}`))
			return
		}
		if r.URL.Path == "/v2/email/suppression/addresses" {
			w.Write([]byte(`{"SuppressedDestinationSummaries":[]}`))
			return
		}
		t.Errorf("unexpected service AWS request %s", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer fake.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	dir := t.TempDir()
	if activeReport {
		db, err := store.Open(filepath.Join(dir, "serve.db"))
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{
			`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Stream','','stream@example.org',1)`,
			`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'stream','sent','Stream','<p>Stream</p>',1,1)`,
			`INSERT INTO campaign_stats(campaign_id) VALUES(1)`,
		} {
			if _, err := db.Writer.Exec(query); err != nil {
				db.Close()
				t.Fatal(err)
			}
		}
		hash := sha256.Sum256([]byte("shutdown.secret"))
		_, err = db.Writer.Exec(`INSERT INTO api_keys(brand_id,name,prefix,secret_hash,scopes,created_at) VALUES(1,'Shutdown','shutdown',?,'["read"]',1)`, hash[:])
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	child := exec.Command(os.Args[0], "-test.run=^TestServeProcess$")
	child.Env = append(os.Environ(), "POSTBOX_TEST_SERVE_CHILD=1", "POSTBOX_DB="+filepath.Join(dir, "serve.db"), "POSTBOX_SECRET_FILE="+filepath.Join(dir, "tracking.secret"), "POSTBOX_BACKUP_DIR="+filepath.Join(dir, "backups"), "POSTBOX_LISTEN="+address, "POSTBOX_BASE_URL=http://localhost:"+strings.Split(address, ":")[1], "POSTBOX_DEVELOPMENT=true", "POSTBOX_SECURE_COOKIES=false", "POSTBOX_AWS_REGION=eu-west-1", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_SESSION_TOKEN=", "AWS_EC2_METADATA_DISABLED=true", "AWS_ENDPOINT_URL="+fake.URL, "POSTBOX_NOTIFICATION_TO=", "POSTBOX_SHUTDOWN_TIMEOUT=5s")
	child.Stdout = &output
	child.Stderr = &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	healthy := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				healthy = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !healthy {
		child.Process.Kill()
		child.Wait()
		t.Fatalf("server never healthy: %s", output.String())
	}
	if activeReport {
		request, err := http.NewRequest("GET", "http://"+address+"/api/v1/campaigns/1/events?brand_id=1", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer shutdown.secret")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("stream status: %d", response.StatusCode)
		}
		line, err := bufio.NewReader(response.Body).ReadString('\n')
		if err != nil || line != "event: stats\n" {
			t.Fatalf("stream event: %q %v", line, err)
		}
		// Keep the live response open until after the child exits.
	}
	shutdownStarted := time.Now()
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v %s", err, output.String())
		}
	case <-time.After(8 * time.Second):
		child.Process.Kill()
		<-done
		t.Fatalf("shutdown exceeded deadline: %s", output.String())
	}
	if activeReport && time.Since(shutdownStarted) >= 3*time.Second {
		t.Fatalf("active report delayed shutdown: %s\n%s", time.Since(shutdownStarted), output.String())
	}
	db, err := store.Open(filepath.Join(dir, "serve.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var enabled string
	if err := db.Reader.QueryRow("SELECT value FROM settings WHERE key='sending_enabled'").Scan(&enabled); err != nil || enabled != "false" {
		t.Fatalf("serve default: %s %v", enabled, err)
	}
}

func TestMissingAWSCredentialsCannotReportDelivery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "missing-config"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MessageId":"must-not-be-accepted"}`))
	}))
	defer server.Close()
	sender, err := send.NewAWS(context.Background(), "eu-west-1", server.URL, 1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := sender.Send(context.Background(), send.Request{From: "sender@example.org", To: "reader@example.org", Raw: []byte("Subject: Test\r\n\r\nTest")})
	if err == nil || id != "" || calls.Load() != 0 {
		t.Fatalf("missing credentials sent: id=%q error=%v calls=%d", id, err, calls.Load())
	}
}
