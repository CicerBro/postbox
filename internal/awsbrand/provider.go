// Package awsbrand stores encrypted brand AWS credentials and resolves isolated SES clients.
package awsbrand

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"postbox/internal/send"
	"postbox/internal/store"
)

// Update is write-only. Blank secret fields preserve saved values unless ClearSessionToken is set.
type Update struct {
	Mode              string  `json:"mode"`
	Region            string  `json:"region"`
	AccessKeyID       string  `json:"access_key_id"`
	SecretAccessKey   string  `json:"secret_access_key"`
	SessionToken      string  `json:"session_token"`
	ClearSessionToken bool    `json:"clear_session_token"`
	TopicARN          *string `json:"sns_topic_arn"`
}

// Settings deliberately contains no credential values, including access key IDs.
type Settings struct {
	Mode            string `json:"mode"`
	Region          string `json:"region"`
	Configured      bool   `json:"configured"`
	HasCredentials  bool   `json:"has_credentials"`
	HasSessionToken bool   `json:"has_session_token"`
	UpdatedAt       int64  `json:"updated_at"`
	TopicARN        string `json:"sns_topic_arn"`
}
type material struct{ AccessKeyID, SecretAccessKey, SessionToken string }
type record struct {
	Settings
	encrypted []byte
	revision  int64
}
type cached struct {
	revision    int64
	fingerprint string
	client      *send.AWS
	scope       string
}
type Provider struct {
	db               *store.DB
	aead             cipher.AEAD
	region, endpoint string
	http             *http.Client
	mu               sync.Mutex
	clients          map[int64]cached
}

func New(db *store.DB, trackingSecret []byte, serverRegion, endpoint string, maxWorkers int) (*Provider, error) {
	if len(trackingSecret) < 32 {
		return nil, errors.New("AWS credential encryption requires a server secret of at least 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, trackingSecret, nil, "postbox/brand-aws/credentials/aes-gcm/v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if maxWorkers < 1 {
		maxWorkers = 1024
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = maxWorkers * 2
	tr.MaxIdleConnsPerHost = maxWorkers
	tr.MaxConnsPerHost = maxWorkers
	return &Provider{db: db, aead: aead, region: serverRegion, endpoint: endpoint, http: &http.Client{Transport: tr, Timeout: 30 * time.Second}, clients: map[int64]cached{}}, nil
}
func (p *Provider) aad(id int64) []byte { return []byte(fmt.Sprintf("postbox/brand-aws/v1/%d", id)) }
func (p *Provider) seal(id int64, m material) ([]byte, error) {
	b, e := json.Marshal(m)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return p.aead.Seal(nonce, nonce, b, p.aad(id)), nil
}
func (p *Provider) open(id int64, b []byte) (material, error) {
	var m material
	if len(b) < p.aead.NonceSize() {
		return m, errors.New("saved AWS credentials cannot be decrypted")
	}
	n := p.aead.NonceSize()
	plain, e := p.aead.Open(nil, b[:n], b[n:], p.aad(id))
	if e != nil {
		return m, errors.New("saved AWS credentials cannot be decrypted")
	}
	if json.Unmarshal(plain, &m) != nil {
		return m, errors.New("saved AWS credentials are invalid")
	}
	return m, nil
}
func read(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (record, error) {
	var r record
	e := q.QueryRowContext(ctx, "SELECT mode,region,credentials,revision,updated_at,sns_topic_arn FROM brand_aws_settings WHERE brand_id=?", id).Scan(&r.Mode, &r.Region, &r.encrypted, &r.revision, &r.UpdatedAt, &r.TopicARN)
	r.Configured = e == nil
	return r, e
}
func (p *Provider) Settings(ctx context.Context, id int64) (Settings, error) {
	r, e := read(ctx, p.db.Reader, id)
	if errors.Is(e, sql.ErrNoRows) {
		return Settings{Mode: "static", Region: p.region}, nil
	}
	if e != nil {
		return Settings{}, e
	}
	m, e := p.open(id, r.encrypted)
	if e != nil {
		return Settings{}, e
	}
	r.HasCredentials = m.AccessKeyID != "" && m.SecretAccessKey != ""
	r.HasSessionToken = m.SessionToken != ""
	return r.Settings, nil
}

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-\d+$`)
var accountPattern = regexp.MustCompile(`^\d{12}$`)

func (p *Provider) Save(ctx context.Context, id int64, u Update) error {
	u.Mode = strings.TrimSpace(u.Mode)
	u.Region = strings.TrimSpace(u.Region)
	if u.Mode != "static" {
		return errors.New("AWS mode must be static")
	}
	if !regionPattern.MatchString(u.Region) {
		return errors.New("an explicit AWS region is required")
	}
	e := p.db.Immediate(ctx, func(c *sql.Conn) error {
		old, e := read(ctx, c, id)
		var m material
		if e == nil && old.Mode == "static" {
			m, e = p.open(id, old.encrypted)
			if e != nil {
				return e
			}
		} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		previous := m
		if u.TopicARN != nil {
			old.TopicARN = strings.TrimSpace(*u.TopicARN)
		}
		if old.TopicARN != "" && !validTopicARN(old.TopicARN, u.Region) {
			return errors.New("SNS topic ARN must match the brand AWS region")
		}
		if u.AccessKeyID != "" {
			if m.AccessKeyID != "" && u.AccessKeyID != m.AccessKeyID && u.SecretAccessKey == "" {
				return errors.New("a new access key requires its secret access key")
			}
			m.AccessKeyID = u.AccessKeyID
		}
		if u.SecretAccessKey != "" {
			m.SecretAccessKey = u.SecretAccessKey
		}
		if u.SessionToken != "" {
			m.SessionToken = u.SessionToken
		}
		if u.ClearSessionToken {
			m.SessionToken = ""
		}
		if m.AccessKeyID == "" || m.SecretAccessKey == "" {
			return errors.New("access key ID and secret access key are required")
		}
		b, e := p.seal(id, m)
		if e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, `INSERT INTO brand_aws_settings(brand_id,mode,region,credentials,updated_at,sns_topic_arn) VALUES(?,?,?,?,?,?) ON CONFLICT(brand_id) DO UPDATE SET mode=excluded.mode,region=excluded.region,credentials=excluded.credentials,sns_topic_arn=excluded.sns_topic_arn,revision=brand_aws_settings.revision+1,updated_at=excluded.updated_at`, id, u.Mode, u.Region, b, time.Now().UnixMilli(), old.TopicARN)
		if e == nil && (!old.Configured || old.Mode != u.Mode || old.Region != u.Region || previous != m) {
			return resetSuppressions(ctx, c, id)
		}
		return e
	})
	if e == nil {
		p.mu.Lock()
		delete(p.clients, id)
		p.mu.Unlock()
	}
	return e
}

func validTopicARN(arn, region string) bool {
	parts := strings.Split(arn, ":")
	return len(parts) == 6 && parts[0] == "arn" && parts[1] == awsPartition(region) && parts[2] == "sns" && parts[3] == region && accountPattern.MatchString(parts[4]) && parts[5] != ""
}

func awsPartition(region string) string {
	if strings.HasPrefix(region, "us-gov-") {
		return "aws-us-gov"
	}
	if strings.HasPrefix(region, "cn-") {
		return "aws-cn"
	}
	return "aws"
}

func (p *Provider) EventBrand(ctx context.Context, topicARN string) (int64, error) {
	var id int64
	err := p.db.Reader.QueryRowContext(ctx, `SELECT brand_id FROM brand_aws_settings WHERE mode='static' AND sns_topic_arn=?`, topicARN).Scan(&id)
	return id, err
}

func (p *Provider) Delete(ctx context.Context, id int64) error {
	e := p.db.Immediate(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "DELETE FROM brand_aws_settings WHERE brand_id=?", id); e != nil {
			return e
		}
		return resetSuppressions(ctx, c, id)
	})
	if e == nil {
		p.mu.Lock()
		delete(p.clients, id)
		p.mu.Unlock()
	}
	return e
}

// A connection change invalidates only imports from the previous AWS account.
// Local suppressions and global manual blocks keep their independent meaning.
func resetSuppressions(ctx context.Context, c *sql.Conn, id int64) error {
	if _, e := c.ExecContext(ctx, "DELETE FROM suppressions WHERE brand_id=? AND reason='ses_sync'", id); e != nil {
		return e
	}
	now := time.Now().UnixMilli()
	if _, e := c.ExecContext(ctx, "UPDATE jobs SET run_at=min(run_at,?) WHERE queue='ses_suppressions' AND status='pending'", now); e != nil {
		return e
	}
	_, e := c.ExecContext(ctx, `INSERT INTO jobs(queue,payload,status,run_at,created_at) SELECT 'ses_suppressions','{}','pending',?,? WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE queue='ses_suppressions' AND status='pending')`, now, now)
	return e
}
func (p *Provider) resolve(ctx context.Context, id int64) (cached, error) {
	// Serialize resolution with cache invalidation. Each dispatch receives a stable client snapshot.
	p.mu.Lock()
	defer p.mu.Unlock()
	r, e := read(ctx, p.db.Reader, id)
	if errors.Is(e, sql.ErrNoRows) {
		return cached{}, errors.New("brand AWS access keys are not configured")
	}
	if e != nil {
		return cached{}, e
	}
	if r.Mode != "static" {
		return cached{}, errors.New("brand AWS access keys are not configured")
	}
	if old, ok := p.clients[id]; ok && old.revision == r.revision {
		if old.fingerprint == fingerprint(r) {
			return old, nil
		}
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(r.Region), config.WithHTTPClient(p.http), config.WithRetryer(func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 1; o.RateLimiter = ratelimit.None })
	})}
	m, e := p.open(id, r.encrypted)
	if e != nil {
		return cached{}, e
	}
	if m.AccessKeyID == "" || m.SecretAccessKey == "" {
		return cached{}, errors.New("brand AWS access keys are not configured")
	}
	opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(m.AccessKeyID, m.SecretAccessKey, m.SessionToken)))
	cfg, e := config.LoadDefaultConfig(ctx, opts...)
	if e != nil {
		return cached{}, errors.New("AWS configuration could not be loaded")
	}
	sc := sesv2.NewFromConfig(cfg, func(o *sesv2.Options) {
		if p.endpoint != "" {
			o.BaseEndpoint = aws.String(p.endpoint)
		}
	})
	st := sts.NewFromConfig(cfg, func(o *sts.Options) {
		if p.endpoint != "" {
			o.BaseEndpoint = aws.String(p.endpoint)
		}
	})
	who, e := st.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if e != nil {
		return cached{}, awsCheckError("sts:GetCallerIdentity", r.Region, e)
	}
	account := aws.ToString(who.Account)
	if account == "" {
		return cached{}, errors.New("AWS account identity is missing")
	}
	v := cached{revision: r.revision, fingerprint: fingerprint(r), client: &send.AWS{Client: sc}, scope: account + ":" + r.Region}
	p.clients[id] = v
	return v, nil
}

// fingerprint binds an import snapshot to the complete encrypted connection.
// Random ciphertext nonces distinguish delete/recreate even when revision resets.
func fingerprint(r record) string {
	h := sha256.New()
	h.Write([]byte(r.Mode))
	h.Write([]byte{0})
	h.Write([]byte(r.Region))
	h.Write([]byte{0})
	h.Write(r.encrypted)
	h.Write([]byte{0})
	h.Write([]byte(r.TopicARN))
	return hex.EncodeToString(h.Sum(nil))
}

// CheckSnapshot must be called inside the transaction that imports SES data.
// A failed check prevents an old account's results from entering a new connection.
func (p *Provider) CheckSnapshot(ctx context.Context, c *sql.Conn, id int64, snapshot string) error {
	r, e := read(ctx, c, id)
	if errors.Is(e, sql.ErrNoRows) {
		return errors.New("brand AWS access keys are not configured")
	}
	if e != nil {
		return e
	}
	if fingerprint(r) != snapshot {
		return errors.New("brand AWS connection changed during synchronization")
	}
	return nil
}
func (p *Provider) ResolveBrand(ctx context.Context, id int64) (send.BrandSender, error) {
	v, e := p.resolve(ctx, id)
	if e != nil {
		return send.BrandSender{}, e
	}
	return send.BrandSender{Sender: v.client, Scope: v.scope}, nil
}
