// Package config loads flags and environment without storing AWS credentials.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DB, Listen, BaseURL, Region, SecretFile, QueueURL, TopicARN, GeoIP, BackupDir, TurnstileSecret, TurnstileSiteKey string
	SecureCookies, Development                                                                                       bool
	MaxWorkers, RetentionDays, SoftBounceLimit                                                                       int
	RecoveryPolicy                                                                                                   string
	SessionTTL, ShutdownTimeout                                                                                      time.Duration
}

func Load(args []string) (Config, error) {
	c := Config{}
	f := flag.NewFlagSet("postbox", flag.ContinueOnError)
	f.StringVar(&c.DB, "db", env("DB", "./data/postbox.db"), "SQLite database file")
	f.StringVar(&c.Listen, "listen", env("LISTEN", "127.0.0.1:8080"), "HTTP listen address")
	f.StringVar(&c.BaseURL, "base-url", env("BASE_URL", ""), "public HTTPS base URL")
	f.StringVar(&c.Region, "region", env("AWS_REGION", os.Getenv("AWS_REGION")), "AWS region")
	f.StringVar(&c.SecretFile, "secret-file", env("SECRET_FILE", ""), "persistent tracking token secret file")
	f.StringVar(&c.QueueURL, "sqs-queue-url", env("SQS_QUEUE_URL", ""), "SES event SQS queue URL")
	f.StringVar(&c.TopicARN, "sns-topic-arn", env("SNS_TOPIC_ARN", ""), "expected SNS topic ARN")
	f.StringVar(&c.GeoIP, "geoip", env("GEOIP", ""), "optional local MaxMind database")
	f.StringVar(&c.BackupDir, "backup-dir", env("BACKUP_DIR", "./data/backups"), "online backup directory")
	f.StringVar(&c.TurnstileSecret, "turnstile-secret", env("TURNSTILE_SECRET", ""), "Cloudflare Turnstile secret")
	// flag prints DefValue in help; never echo a secret loaded from the environment.
	f.Lookup("turnstile-secret").DefValue = ""
	f.StringVar(&c.TurnstileSiteKey, "turnstile-site-key", env("TURNSTILE_SITE_KEY", ""), "Cloudflare Turnstile public site key")
	f.BoolVar(&c.SecureCookies, "secure-cookies", boolEnv("SECURE_COOKIES", true), "require HTTPS session cookies")
	f.BoolVar(&c.Development, "development", boolEnv("DEVELOPMENT", false), "allow local HTTP base URL for development")
	f.IntVar(&c.MaxWorkers, "max-workers", intEnv("MAX_WORKERS", 512), "maximum SES workers")
	f.IntVar(&c.RetentionDays, "retention-days", intEnv("RETENTION_DAYS", 90), "raw event retention days")
	f.IntVar(&c.SoftBounceLimit, "soft-bounce-limit", intEnv("SOFT_BOUNCE_LIMIT", 3), "soft bounce suppression threshold")
	f.StringVar(&c.RecoveryPolicy, "recovery-policy", env("RECOVERY_POLICY", "unknown"), "claimed recipient recovery policy: unknown (uncertain attempts cannot be retried)")
	f.DurationVar(&c.SessionTTL, "session-ttl", durationEnv("SESSION_TTL", 24*time.Hour), "admin session lifetime")
	f.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", durationEnv("SHUTDOWN_TIMEOUT", 60*time.Second), "graceful shutdown timeout")
	if v := env("AMBIGUOUS_POLICY", ""); v != "" && v != "unknown" {
		return c, errors.New("ambiguous send retries are prohibited")
	}
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if f.NArg() != 0 {
		return c, fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	if c.SecretFile == "" {
		c.SecretFile = c.DB + ".secret"
	}
	if c.MaxWorkers < 1 || c.RetentionDays < 1 || c.SoftBounceLimit < 1 || c.SessionTTL <= 0 || c.ShutdownTimeout <= 0 {
		return c, errors.New("worker, retention, bounce and duration values must be positive")
	}
	if c.RecoveryPolicy != "unknown" {
		return c, errors.New("recovery policy must be unknown; retrying uncertain sends is prohibited")
	}
	return c, nil
}
func (c Config) ValidateServe() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return errors.New("base-url must be an absolute public origin, for example https://mail.example.com")
	}
	if u.Scheme != "https" && !(c.Development && u.Scheme == "http") {
		return errors.New("production base-url requires HTTPS; local HTTP requires --development")
	}
	if !c.Development && !c.SecureCookies {
		return errors.New("production requires secure cookies")
	}
	return nil
}
func TokenSecret(path string) ([]byte, error) {
	decode := func(data []byte) ([]byte, error) {
		b, e := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if e != nil || len(b) != 32 {
			return nil, errors.New("tracking secret must contain 32 base64-encoded bytes")
		}
		return b, nil
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("tracking secret %s must be a regular file", path)
		}
		if info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("tracking secret %s must have permissions 0600", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return decode(data)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return TokenSecret(path)
	}
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(base64.RawStdEncoding.EncodeToString(secret) + "\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return secret, nil
}
func env(k, d string) string {
	if v, ok := os.LookupEnv("POSTBOX_" + k); ok {
		return v
	}
	return d
}
func boolEnv(k string, d bool) bool {
	v := env(k, "")
	if v == "" {
		return d
	}
	b, e := strconv.ParseBool(v)
	if e != nil {
		return d
	}
	return b
}
func intEnv(k string, d int) int {
	v := env(k, "")
	if v == "" {
		return d
	}
	n, e := strconv.Atoi(v)
	if e != nil {
		return 0
	}
	return n
}
func durationEnv(k string, d time.Duration) time.Duration {
	v := env(k, "")
	if v == "" {
		return d
	}
	n, e := time.ParseDuration(v)
	if e != nil {
		return 0
	}
	return n
}
