package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"postbox/internal/store"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	TopicARN        string
	BrandID         int64
	ResolveTopic    func(context.Context, string) (int64, error)
	SoftBounceLimit int
	HTTPClient      *http.Client
	Logger          *slog.Logger
}
type Service struct {
	db         *store.DB
	cfg        Config
	Verifier   *Verifier
	verifiers  sync.Map
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
	if c.Logger == nil {
		c.Logger = slog.Default()
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
	stage := "decode"
	brandID := s.cfg.BrandID
	verifier := s.Verifier
	if e == nil && s.cfg.ResolveTopic != nil {
		stage = "topic_lookup"
		brandID, e = s.cfg.ResolveTopic(r.Context(), m.TopicARN)
		v, _ := s.verifiers.LoadOrStore(m.TopicARN, &Verifier{TopicARN: m.TopicARN, Client: s.cfg.HTTPClient})
		verifier = v.(*Verifier)
	}
	if e == nil {
		stage = "signature_verification"
		e = verifier.Verify(r.Context(), m)
	}
	if e != nil {
		s.logSNSFailure(r.Context(), stage, e)
		http.Error(w, "invalid SNS signature", 403)
		return
	}
	if m.Type == "SubscriptionConfirmation" {
		e = verifier.Confirm(r.Context(), m)
	} else if m.Type == "Notification" {
		e = s.process(r.Context(), brandID, m.MessageID, []byte(m.Message))
	} else {
		e = errors.New("unsupported event")
	}
	if e != nil {
		if m.Type == "SubscriptionConfirmation" {
			s.logSNSFailure(r.Context(), "subscription_confirmation", e)
		}
		http.Error(w, "event processing failed", 503)
		return
	}
	w.WriteHeader(204)
}

// Transport errors include the requested URL, which can contain a confirmation
// token. Log only the underlying cause, never the SNS payload or URL.
func (s *Service) logSNSFailure(ctx context.Context, stage string, err error) {
	var transportError *url.Error
	for errors.As(err, &transportError) {
		err = transportError.Err
	}
	s.cfg.Logger.WarnContext(ctx, "SNS callback failed", "stage", stage, "error", err)
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
	return s.process(ctx, s.cfg.BrandID, id, payload)
}

func (s *Service) process(ctx context.Context, brandID int64, id string, payload []byte) error {
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
	if brandID != 0 && firstTag(ev.Mail.Tags, "brand_id") != fmt.Sprint(brandID) {
		return errors.New("SES event brand tag does not match SNS topic")
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
		var e error
		brandID, e = resolveEventBrandTx(ctx, c, brandID, ev)
		if e != nil {
			return e
		}
		r, e := c.ExecContext(ctx, `INSERT OR IGNORE INTO ses_events(sns_message_id,ses_message_id,type,subtype,recipient,at,payload) VALUES(?,?,?,?,?,?,?)`, id, ev.Mail.MessageID, typ, ev.Bounce.BounceType, strings.Join(recipients, ","), at, string(payload))
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			duplicate = true
			return nil
		}
		seen := make(map[string]bool, len(recipients))
		for _, email := range recipients {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" || seen[email] {
				continue
			}
			seen[email] = true
			match, err := matchRecipientTx(ctx, c, brandID, ev, email)
			if err != nil {
				return err
			}
			if match.subscriber != 0 {
				if err = recordRecipientEventTx(ctx, c, match, ev, typ, at); err != nil {
					return err
				}
			}
			if err = s.applySuppressionTx(ctx, c, brandID, match, email, ev, typ, at); err != nil {
				return err
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
