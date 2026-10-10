package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"io"
	"net/http"
	"postbox/internal/jobs"
	"postbox/internal/store"
	"strings"
	"sync/atomic"
	"time"
)

type Config struct {
	TopicARN, QueueURL string
	SoftBounceLimit    int
	HTTPClient         *http.Client
}
type Service struct {
	db         *store.DB
	cfg        Config
	Verifier   *Verifier
	processed  atomic.Uint64
	duplicates atomic.Uint64
	failures   atomic.Uint64
}

func New(db *store.DB, cfg *Config) *Service {
	var c Config
	if cfg != nil {
		c = *cfg
	}
	if c.SoftBounceLimit <= 0 {
		c.SoftBounceLimit = 3
	}
	return &Service{db: db, cfg: c, Verifier: &Verifier{TopicARN: c.TopicARN, Client: c.HTTPClient}}
}
func (s *Service) Register(mux *http.ServeMux) { mux.HandleFunc("POST /events/sns", s.SNS) }
func (s *Service) SNS(w http.ResponseWriter, r *http.Request) {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	m, e := DecodeSNS(b)
	if e == nil {
		e = s.Verifier.Verify(r.Context(), m)
	}
	if e != nil {
		http.Error(w, "invalid SNS signature", 403)
		return
	}
	if m.Type == "SubscriptionConfirmation" {
		e = s.Verifier.Confirm(r.Context(), m)
	} else if m.Type == "Notification" {
		e = s.Process(r.Context(), m.MessageID, []byte(m.Message))
	} else {
		e = errors.New("unsupported event")
	}
	if e != nil {
		http.Error(w, "event processing failed", 503)
		return
	}
	w.WriteHeader(204)
}

type sesEvent struct {
	EventType        string `json:"eventType"`
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID   string `json:"messageId"`
		Destination []string
		Tags        map[string][]string
	}
	Bounce struct {
		BounceType        string
		Timestamp         string
		BouncedRecipients []struct{ EmailAddress string }
	}
	Complaint struct {
		Timestamp            string
		ComplainedRecipients []struct{ EmailAddress string }
	}
	Delivery struct {
		Timestamp  string
		Recipients []string
	}
}

func (s *Service) Process(ctx context.Context, id string, payload []byte) error {
	var ev sesEvent
	if e := json.Unmarshal(payload, &ev); e != nil {
		return e
	}
	typ := ev.EventType
	if typ == "" {
		typ = ev.NotificationType
	}
	if id == "" || ev.Mail.MessageID == "" || typ == "" {
		return errors.New("missing SES event identifiers")
	}
	recipients := ev.Mail.Destination
	timestamp := ""
	switch typ {
	case "Bounce":
		recipients = nil
		timestamp = ev.Bounce.Timestamp
		for _, r := range ev.Bounce.BouncedRecipients {
			recipients = append(recipients, r.EmailAddress)
		}
	case "Complaint":
		recipients = nil
		timestamp = ev.Complaint.Timestamp
		for _, r := range ev.Complaint.ComplainedRecipients {
			recipients = append(recipients, r.EmailAddress)
		}
	case "Delivery":
		recipients = ev.Delivery.Recipients
		timestamp = ev.Delivery.Timestamp
	}
	at := time.Now().UnixMilli()
	if t, e := time.Parse(time.RFC3339, timestamp); e == nil {
		at = t.UnixMilli()
	}
	duplicate := false
	err := s.db.Immediate(ctx, func(c *sql.Conn) error {
		r, e := c.ExecContext(ctx, `INSERT OR IGNORE INTO ses_events(sns_message_id,ses_message_id,type,subtype,recipient,at,payload) VALUES(?,?,?,?,?,?,?)`, id, ev.Mail.MessageID, typ, ev.Bounce.BounceType, strings.Join(recipients, ","), at, string(payload))
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			duplicate = true
			return nil
		}
		for _, email := range recipients {
			email = strings.ToLower(strings.TrimSpace(email))
			var campaign, subscriber int64
			e = c.QueryRowContext(ctx, `SELECT campaign_id,subscriber_id FROM campaign_recipients WHERE message_id=?`, ev.Mail.MessageID).Scan(&campaign, &subscriber)
			if errors.Is(e, sql.ErrNoRows) {
				campaignTag := firstTag(ev.Mail.Tags, "campaign_id", "campaign")
				brandTag := firstTag(ev.Mail.Tags, "brand_id", "brand")
				e = c.QueryRowContext(ctx, `SELECT cr.campaign_id,cr.subscriber_id FROM campaign_recipients cr JOIN subscribers s ON s.id=cr.subscriber_id WHERE cr.campaign_id=? AND cr.snapshot_email=? AND s.brand_id=?`, campaignTag, email, brandTag).Scan(&campaign, &subscriber)
				if errors.Is(e, sql.ErrNoRows) {
					e = c.QueryRowContext(ctx, `SELECT cr.campaign_id,cr.subscriber_id FROM subscribers s JOIN campaign_recipients cr ON cr.subscriber_id=s.id WHERE s.brand_id=? AND s.email=? AND cr.campaign_id=? AND cr.snapshot_email IS NULL`, brandTag, email, campaignTag).Scan(&campaign, &subscriber)
				}
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			matched := e == nil
			if matched {
				var snapshot string
				if e = c.QueryRowContext(ctx, `SELECT coalesce(cr.snapshot_email,s.email) FROM campaign_recipients cr JOIN subscribers s ON s.id=cr.subscriber_id WHERE cr.campaign_id=? AND cr.subscriber_id=?`, campaign, subscriber).Scan(&snapshot); e != nil {
					return e
				}
				if snapshot != email {
					matched = false
				}
			}
			if matched {
				if _, e = c.ExecContext(ctx, `INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) SELECT brand_id,?,?,?,json_object('campaign_id',?,'ses_message_id',?,'subtype',?) FROM campaigns WHERE id=?`, subscriber, strings.ToLower(typ), at, campaign, ev.Mail.MessageID, ev.Bounce.BounceType, campaign); e != nil {
					return e
				}
			}
			if matched {
				if _, e = c.ExecContext(ctx, `INSERT OR IGNORE INTO campaign_stats(campaign_id) VALUES(?)`, campaign); e != nil {
					return e
				}
				switch typ {
				case "Delivery":
					r, e = c.ExecContext(ctx, `UPDATE campaign_recipients SET delivered_at=? WHERE campaign_id=? AND subscriber_id=? AND delivered_at IS NULL`, at, campaign, subscriber)
					if e != nil {
						return e
					}
					n, _ = r.RowsAffected()
					if n > 0 {
						_, e = c.ExecContext(ctx, `UPDATE campaign_stats SET delivered=delivered+1 WHERE campaign_id=?`, campaign)
						if e == nil {
							_, e = c.ExecContext(ctx, `INSERT INTO engagement_hourly(campaign_id,hour,deliveries) VALUES(?,?,1) ON CONFLICT(campaign_id,hour) DO UPDATE SET deliveries=deliveries+1`, campaign, at/3600000*3600000)
						}
					}
				case "Bounce":
					r, e = c.ExecContext(ctx, `UPDATE campaign_recipients SET bounced_at=coalesce(bounced_at,?) WHERE campaign_id=? AND subscriber_id=?`, at, campaign, subscriber)
					if e == nil {
						col := "bounces_soft"
						if ev.Bounce.BounceType == "Permanent" {
							col = "bounces_hard"
						}
						_, e = c.ExecContext(ctx, `UPDATE campaign_stats SET `+col+`=`+col+`+1 WHERE campaign_id=?`, campaign)
					}
				case "Complaint":
					r, e = c.ExecContext(ctx, `UPDATE campaign_recipients SET complained_at=? WHERE campaign_id=? AND subscriber_id=? AND complained_at IS NULL`, at, campaign, subscriber)
					if e == nil {
						n, _ = r.RowsAffected()
						if n > 0 {
							_, e = c.ExecContext(ctx, `UPDATE campaign_stats SET complaints=complaints+1 WHERE campaign_id=?`, campaign)
						}
					}
				}
				if e != nil {
					return e
				}
			}
			reason := ""
			status := "bounced"
			if typ == "Complaint" {
				reason = "complaint"
				status = "complained"
			} else if typ == "Bounce" && ev.Bounce.BounceType == "Permanent" {
				reason = "hard_bounce"
			} else if typ == "Bounce" && ev.Bounce.BounceType == "Transient" {
				if _, e = c.ExecContext(ctx, `UPDATE subscribers SET soft_bounce_count=soft_bounce_count+1,updated_at=? WHERE email=? OR id=?`, at, email, matchedID(matched, subscriber)); e != nil {
					return e
				}
				var count int
				if e = c.QueryRowContext(ctx, `SELECT coalesce(max(soft_bounce_count),0) FROM subscribers WHERE email=? OR id=?`, email, matchedID(matched, subscriber)).Scan(&count); e != nil {
					return e
				}
				if count >= s.cfg.SoftBounceLimit {
					reason = "soft_bounce_limit"
				}
			}
			if reason == "" && typ == "Bounce" && matched {
				var brand int64
				if e = c.QueryRowContext(ctx, `SELECT brand_id FROM campaigns WHERE id=?`, campaign).Scan(&brand); e != nil {
					return e
				}
				if e = jobs.EmitTx(ctx, c, brand, "bounce", map[string]any{"subscriber_id": subscriber, "email": email, "campaign_id": campaign, "reason": "transient"}); e != nil {
					return e
				}
			}
			if reason != "" {
				if _, e = c.ExecContext(ctx, `INSERT OR IGNORE INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(NULL,'email',?,?,?)`, email, reason, at); e != nil {
					return e
				}
				if _, e = c.ExecContext(ctx, `UPDATE subscribers SET status=CASE WHEN status='complained' THEN status ELSE ? END,updated_at=? WHERE email=? OR id=?`, status, at, email, matchedID(matched, subscriber)); e != nil {
					return e
				}
				rows, err := c.QueryContext(ctx, `SELECT id,brand_id FROM subscribers WHERE email=? OR id=?`, email, matchedID(matched, subscriber))
				if err != nil {
					return err
				}
				type identity struct{ id, brand int64 }
				var ids []identity
				for rows.Next() {
					var i identity
					if err = rows.Scan(&i.id, &i.brand); err != nil {
						rows.Close()
						return err
					}
					ids = append(ids, i)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				for _, i := range ids {
					event := "bounce"
					if typ == "Complaint" {
						event = "complaint"
					}
					if err = skipSubscriber(ctx, c, i.id); err != nil {
						return err
					}
					if err = UnsubscribeMembershipsTx(ctx, c, i.id, 0, at, event); err != nil {
						return err
					}
					if err = jobs.EmitTx(ctx, c, i.brand, event, map[string]any{"subscriber_id": i.id, "email": email, "campaign_id": campaign, "reason": reason}); err != nil {
						return err
					}
				}
				if e = skipEmail(ctx, c, email); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if err != nil {
		s.failures.Add(1)
	} else if duplicate {
		s.duplicates.Add(1)
	} else {
		s.processed.Add(1)
	}
	return err
}

type Metrics struct {
	Processed  uint64 `json:"processed"`
	Duplicates uint64 `json:"duplicates"`
	Failures   uint64 `json:"failures"`
}

func (s *Service) Metrics() Metrics {
	return Metrics{s.processed.Load(), s.duplicates.Load(), s.failures.Load()}
}
func firstTag(m map[string][]string, keys ...string) string {
	for _, k := range keys {
		if len(m[k]) > 0 {
			return m[k][0]
		}
	}
	return ""
}
func skipEmail(ctx context.Context, c *sql.Conn, email string) error {
	rows, e := c.QueryContext(ctx, `UPDATE campaign_recipients SET status='skipped',error='suppressed' WHERE subscriber_id IN(SELECT id FROM subscribers WHERE email=?) AND status='pending' AND campaign_id IN(SELECT id FROM campaigns WHERE status IN('preparing','sending','paused')) RETURNING campaign_id`, email)
	if e != nil {
		return e
	}
	counts := map[int64]int{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		counts[id]++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for id, n := range counts {
		if _, e = c.ExecContext(ctx, `UPDATE campaign_stats SET skipped=skipped+? WHERE campaign_id=?`, n, id); e != nil {
			return e
		}
	}
	return nil
}

type SQSClient interface {
	ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

func (s *Service) Poll(ctx context.Context, client SQSClient) error {
	if s.cfg.QueueURL == "" {
		return errors.New("SQS queue URL required")
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		out, e := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: aws.String(s.cfg.QueueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 20, VisibilityTimeout: 120})
		if e != nil {
			return fmt.Errorf("SQS receive: %w", e)
		}
		for _, m := range out.Messages {
			envelope, e := DecodeSNS([]byte(aws.ToString(m.Body)))
			if e != nil || envelope.Type != "Notification" || envelope.TopicARN != s.cfg.TopicARN {
				continue
			}
			if e = s.Process(ctx, envelope.MessageID, []byte(envelope.Message)); e != nil {
				continue
			}
			if _, e = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: aws.String(s.cfg.QueueURL), ReceiptHandle: m.ReceiptHandle}); e != nil {
				return fmt.Errorf("SQS delete committed event: %w", e)
			}
		}
	}
}

func matchedID(matched bool, id int64) int64 {
	if matched {
		return id
	}
	return 0
}
func skipSubscriber(ctx context.Context, c *sql.Conn, id int64) error {
	rows, e := c.QueryContext(ctx, `UPDATE campaign_recipients SET status='skipped',error='suppressed' WHERE subscriber_id=? AND status='pending' AND campaign_id IN(SELECT id FROM campaigns WHERE status IN('preparing','sending','paused')) RETURNING campaign_id`, id)
	if e != nil {
		return e
	}
	counts := map[int64]int{}
	for rows.Next() {
		var cid int64
		if e = rows.Scan(&cid); e != nil {
			rows.Close()
			return e
		}
		counts[cid]++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for cid, n := range counts {
		if _, e = c.ExecContext(ctx, `UPDATE campaign_stats SET skipped=skipped+? WHERE campaign_id=?`, n, cid); e != nil {
			return e
		}
	}
	return nil
}
