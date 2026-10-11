package tracking

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oschwald/maxminddb-golang"
	"html"
	"net"
	"net/http"
	"net/url"
	"postbox/internal/consent"
	"postbox/internal/htmlpolicy"
	"postbox/internal/jobs"
	"postbox/internal/render"
	"postbox/internal/store"
	"postbox/internal/subscribers"
	"postbox/internal/tracking/token"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	TokenSecret          []byte
	BaseURL              string
	QueueSize, BatchSize int
	FlushInterval        time.Duration
	GeoIPPath            string
}
type record struct {
	campaign, subscriber, link, at int64
	machine                        bool
	country                        string
	raw                            bool
}
type Service struct {
	db           *store.DB
	cfg          Config
	queue        chan record
	done         chan struct{}
	once         sync.Once
	mu           sync.RWMutex
	links        map[int64]map[int64]string
	geo          *maxminddb.Reader
	DroppedOpens atomic.Uint64
	err          error
}

func New(db *store.DB, cfg *Config) *Service {
	var c Config
	if cfg != nil {
		c = *cfg
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 4096
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 500
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 250 * time.Millisecond
	}
	return &Service{db: db, cfg: c, queue: make(chan record, c.QueueSize), done: make(chan struct{}), links: map[int64]map[int64]string{}}
}
func (s *Service) Register(m *http.ServeMux) {
	m.HandleFunc("GET /o/{token}", s.Open)
	m.HandleFunc("GET /c/{link}/{token}", s.Click)
	m.HandleFunc("GET /w/{campaign}", s.Web)
	m.HandleFunc("GET /w/{campaign}/{token}", s.Web)
	m.HandleFunc("GET /u/{token}", s.Goodbye)
	m.HandleFunc("POST /u/{token}", s.Unsubscribe)
	m.HandleFunc("POST /u/{token}/resubscribe", s.Resubscribe)
}
func (s *Service) Start(ctx context.Context) error {
	if s.cfg.GeoIPPath != "" {
		g, e := maxminddb.Open(s.cfg.GeoIPPath)
		if e != nil {
			return e
		}
		s.geo = g
	}
	s.once.Do(func() { go s.writer(ctx) })
	return nil
}
func (s *Service) Close(ctx context.Context) error {
	select {
	case <-s.done:
		if s.geo != nil {
			s.geo.Close()
		}
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) identity(r *http.Request) (int64, int64, error) {
	if len(s.cfg.TokenSecret) < 32 {
		return 0, 0, errors.New("tracking secret must contain at least 32 bytes")
	}
	c, u, e := token.Decode(s.cfg.TokenSecret, r.PathValue("token"))
	if e != nil {
		return 0, 0, e
	}
	if e = s.host(r, c); e != nil {
		return 0, 0, e
	}
	return c, u, nil
}
func (s *Service) host(r *http.Request, c int64) error {
	var domain sql.NullString
	var tracking sql.NullString
	e := s.db.Reader.QueryRowContext(r.Context(), `SELECT b.tracking_domain,p.tracking_url FROM campaigns c JOIN brands b ON b.id=c.brand_id LEFT JOIN campaign_preparations p ON p.campaign_id=c.id WHERE c.id=?`, c).Scan(&domain, &tracking)
	if e != nil {
		return e
	}
	host := strings.ToLower(r.Host)
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	base, _ := url.Parse(s.cfg.BaseURL)
	saved, _ := url.Parse(tracking.String)
	if host == strings.ToLower(base.Hostname()) && host != "" || saved != nil && host == strings.ToLower(saved.Hostname()) && host != "" || host == strings.ToLower(domain.String) && host != "" {
		return nil
	}
	return errors.New("tracking host does not match brand")
}
func (s *Service) event(r *http.Request, c, u, l int64) record {
	at := time.Now().UnixMilli()
	agent := strings.ToLower(r.UserAgent())
	machine := strings.Contains(agent, "bot") || strings.Contains(agent, "scanner") || strings.Contains(agent, "googleimageproxy") || strings.Contains(agent, "proofpoint") || strings.Contains(agent, "mimecast") || strings.Contains(agent, "apple") && strings.Contains(agent, "mail")
	var sent sql.NullInt64
	if s.db.Reader.QueryRowContext(r.Context(), `SELECT sent_at FROM campaign_recipients WHERE campaign_id=? AND subscriber_id=?`, c, u).Scan(&sent) == nil && sent.Valid && at-sent.Int64 < 5000 {
		machine = true
	}
	country := ""
	if s.geo != nil {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		var loc struct {
			Country struct {
				ISO string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if s.geo.Lookup(net.ParseIP(ip), &loc) == nil {
			country = loc.Country.ISO
		}
	}
	return record{c, u, l, at, machine, country, true}
}

var pixel, _ = base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")

func (s *Service) Open(w http.ResponseWriter, r *http.Request) {
	c, u, e := s.identity(r)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	ev := s.event(r, c, u, 0)
	select {
	case s.queue <- ev:
	default:
		s.DroppedOpens.Add(1)
		ev.raw = false
		if e = s.writeBatch(r.Context(), []record{ev}); e != nil {
			http.Error(w, "tracking unavailable", 503)
			return
		}
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(pixel)
}
func (s *Service) link(ctx context.Context, c, l int64) (string, error) {
	s.mu.RLock()
	links, ok := s.links[c]
	s.mu.RUnlock()
	if !ok {
		rows, e := s.db.Reader.QueryContext(ctx, `SELECT id,url FROM links WHERE campaign_id=?`, c)
		if e != nil {
			return "", e
		}
		links = map[int64]string{}
		for rows.Next() {
			var id int64
			var u string
			if e = rows.Scan(&id, &u); e != nil {
				rows.Close()
				return "", e
			}
			links[id] = u
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
		s.mu.Lock()
		if len(s.links) >= 128 {
			s.links = map[int64]map[int64]string{}
		}
		s.links[c] = links
		s.mu.Unlock()
	}
	dest, ok := links[l]
	if !ok {
		return "", sql.ErrNoRows
	}
	u, e := url.Parse(dest)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("unsafe destination")
	}
	return dest, nil
}
func (s *Service) Click(w http.ResponseWriter, r *http.Request) {
	c, u, e := s.identity(r)
	l, _ := strconv.ParseInt(r.PathValue("link"), 10, 64)
	if e != nil || l <= 0 {
		http.NotFound(w, r)
		return
	}
	dest, e := s.link(r.Context(), c, l)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	var recipient render.Recipient
	var fieldsJSON, allowedJSON string
	e = s.db.Reader.QueryRowContext(r.Context(), `SELECT cr.snapshot_email,cr.snapshot_name,cr.snapshot_fields,p.fields FROM campaign_recipients cr JOIN campaign_preparations p ON p.campaign_id=cr.campaign_id WHERE cr.campaign_id=? AND cr.subscriber_id=?`, c, u).Scan(&recipient.Email, &recipient.Name, &fieldsJSON, &allowedJSON)
	if e == nil {
		recipient.Fields, e = stringFields(fieldsJSON)
	}
	var allowed []string
	if e == nil {
		e = json.Unmarshal([]byte(allowedJSON), &allowed)
	}
	if e == nil {
		dest, e = render.ResolveLink(dest, recipient, allowed)
	}
	if e != nil {
		http.NotFound(w, r)
		return
	}
	ev := s.event(r, c, u, l)
	select {
	case s.queue <- ev:
	default:
		if e = s.writeBatch(r.Context(), []record{ev}); e != nil {
			http.Error(w, "tracking unavailable", 503)
			return
		}
	}
	htmlpolicy.Protect(w, "no-store")
	http.Redirect(w, r, dest, http.StatusFound)
}
func (s *Service) Web(w http.ResponseWriter, r *http.Request) {
	c, e := strconv.ParseInt(r.PathValue("campaign"), 10, 64)
	if e != nil || s.host(r, c) != nil {
		http.NotFound(w, r)
		return
	}
	recipient := render.Recipient{}
	if r.PathValue("token") != "" {
		tc, u, e := s.identity(r)
		if e != nil || tc != c {
			http.NotFound(w, r)
			return
		}
		var fields string
		e = s.db.Reader.QueryRowContext(r.Context(), `SELECT snapshot_email,snapshot_name,snapshot_fields FROM campaign_recipients WHERE campaign_id=? AND subscriber_id=?`, c, u).Scan(&recipient.Email, &recipient.Name, &fields)
		if e == nil {
			recipient.Fields, e = stringFields(fields)
		}
		if e != nil {
			http.NotFound(w, r)
			return
		}
		recipient.ID = u
		recipient.Token = r.PathValue("token")
	}
	var subject, body, text, fields, base string
	e = s.db.Reader.QueryRowContext(r.Context(), `SELECT subject,html,text,fields,tracking_url FROM campaign_preparations WHERE campaign_id=?`, c).Scan(&subject, &body, &text, &fields, &base)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	var allowed []string
	if e = json.Unmarshal([]byte(fields), &allowed); e != nil {
		http.Error(w, "invalid prepared campaign", 500)
		return
	}
	p, e := render.Restore(subject, body, text, nil, allowed)
	if e != nil {
		http.Error(w, "invalid prepared campaign", 500)
		return
	}
	if recipient.Token != "" {
		recipient.UnsubscribeURL = base + "/u/" + recipient.Token
		recipient.WebVersionURL = fmt.Sprintf("%s/w/%d/%s", base, c, recipient.Token)
	}
	_, body, _, e = p.Render(recipient)
	if e != nil {
		http.Error(w, "render failed", 500)
		return
	}
	if recipient.Token == "" {
		rows, err := s.db.Reader.QueryContext(r.Context(), `SELECT id,url FROM links WHERE campaign_id=?`, c)
		if err != nil {
			http.Error(w, "archive unavailable", 503)
			return
		}
		for rows.Next() {
			var link int64
			var destination string
			if err = rows.Scan(&link, &destination); err != nil {
				rows.Close()
				http.Error(w, "archive unavailable", 503)
				return
			}
			resolved, err := render.ResolveLink(destination, recipient, allowed)
			if err == nil {
				body = strings.ReplaceAll(body, html.EscapeString(base+"/c/"+strconv.FormatInt(link, 10)+"/"), html.EscapeString(resolved))
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			http.Error(w, "archive unavailable", 503)
			return
		}
	}
	htmlpolicy.Set(w, htmlpolicy.Email, "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write([]byte(body))
}
func (s *Service) Goodbye(w http.ResponseWriter, r *http.Request) {
	c, u, e := s.identity(r)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	var brandName string
	if e = s.db.Reader.QueryRowContext(r.Context(), `SELECT b.name FROM brands b JOIN subscribers s ON s.brand_id=b.id WHERE s.id=?`, u).Scan(&brandName); e != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	lists, e := currentMailingLists(r.Context(), s.db.Reader, c, u)
	if e != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	htmlpolicy.Set(w, htmlpolicy.Preferences, "no-store")
	var goodbye string
	_ = s.db.Reader.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key='brand.'||(SELECT brand_id FROM subscribers WHERE id=?)||'.goodbye_html'`, u).Scan(&goodbye)
	if goodbye == "" {
		goodbye = `<h1>Unsubscribe</h1><p class="muted">Stop receiving emails from this sender.</p>`
	}
	preferencePageStart(w, "Unsubscribe", brandName)
	fmt.Fprint(w, goodbye+`<form method="post"><div class="actions">`)
	if len(lists) > 0 {
		label := "Unsubscribe from this list"
		if len(lists) > 1 {
			label = "Unsubscribe from these lists"
		}
		fmt.Fprintf(w, `<button class="primary" name="list" value="current">%s</button>`, label)
		fmt.Fprint(w, `<button name="list" value="all">Unsubscribe from all emails</button>`)
	} else {
		fmt.Fprint(w, `<button class="primary" name="list" value="all">Unsubscribe from all emails</button>`)
	}
	fmt.Fprint(w, `</div></form>`)
	preferencePageEnd(w)
}
func (s *Service) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	c, u, e := s.identity(r)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if e = r.ParseForm(); e != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	list := r.FormValue("list")
	var id int64
	var currentCount int
	if list != "" && list != "all" && list != "current" {
		id, e = strconv.ParseInt(list, 10, 64)
		if e != nil || id <= 0 {
			http.Error(w, "invalid list", 400)
			return
		}
	}
	at := time.Now().UnixMilli()
	e = s.db.Immediate(r.Context(), func(conn *sql.Conn) error {
		var brand int64
		if e = conn.QueryRowContext(r.Context(), `SELECT c.brand_id FROM campaigns c JOIN campaign_recipients cr ON cr.campaign_id=c.id WHERE cr.campaign_id=? AND cr.subscriber_id=?`, c, u).Scan(&brand); e != nil {
			return e
		}
		ids := []int64{id}
		if list == "current" {
			ids, e = currentMailingLists(r.Context(), conn, c, u)
			if e != nil {
				return e
			}
			if len(ids) == 0 {
				return errors.New("campaign has no matching lists")
			}
			currentCount = len(ids)
			if len(ids) == 1 {
				id = ids[0]
			}
		}
		for _, id := range ids {
			if id > 0 {
				var found int
				if e = conn.QueryRowContext(r.Context(), `SELECT 1 FROM lists WHERE id=? AND brand_id=?`, id, brand).Scan(&found); e != nil {
					return e
				}
			}
			if id == 0 && list != "current" {
				if e = consent.RecordBrandOptOutTx(r.Context(), conn, brand, u, at); e != nil {
					return e
				}
			}

			if _, e = consent.UnsubscribeMembershipsTx(r.Context(), conn, u, id, at, "unsubscribe"); e != nil {
				return e
			}
			if _, e = conn.ExecContext(r.Context(), `INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(?,?,'unsubscribe',?,json_object('campaign_id',?,'list_id',?))`, brand, u, at, c, id); e != nil {
				return e
			}
			if e = jobs.EmitTx(r.Context(), conn, brand, "unsubscribe", map[string]any{"subscriber_id": u, "campaign_id": c, "list_id": id}); e != nil {
				return e
			}
			if e = skipAfterUnsubscribe(r.Context(), conn, u, id); e != nil {
				return e
			}
		}

		res, e := conn.ExecContext(r.Context(), `UPDATE campaign_recipients SET unsubscribed_at=? WHERE campaign_id=? AND subscriber_id=? AND unsubscribed_at IS NULL`, at, c, u)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			if e = s.goodbyeEmail(r.Context(), conn, brand, u, id); e != nil {
				return e
			}
		}
		if _, e = conn.ExecContext(r.Context(), `UPDATE campaign_stats SET unsubscribes=unsubscribes+? WHERE campaign_id=?`, n, c); e != nil {
			return e
		}
		return nil
	})
	if e != nil {
		http.Error(w, "unsubscribe could not be saved", 503)
		return
	}
	var brand int64
	var brandName string
	if e = s.db.Reader.QueryRowContext(r.Context(), `SELECT b.id,b.name FROM brands b JOIN subscribers s ON s.brand_id=b.id WHERE s.id=?`, u).Scan(&brand, &brandName); e != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	body, _ := subscribers.NotificationSetting(r.Context(), s.db.Reader, brand, id, "goodbye_html")
	htmlpolicy.Set(w, htmlpolicy.Custom, "no-store")
	if body == "" {
		message := "You have been unsubscribed from all emails from this sender. You can close this page."
		if id > 0 || list == "current" {
			message = "You have been unsubscribed from this list. Your other subscriptions are unchanged."
			if currentCount > 1 {
				message = "You have been unsubscribed from the lists associated with this email. Your other subscriptions are unchanged."
			}
		}
		preferenceConfirmation(w, "You're unsubscribed", message, brandName)
		return
	}
	fmt.Fprint(w, body)
}
func (s *Service) Resubscribe(w http.ResponseWriter, r *http.Request) {
	_, u, e := s.identity(r)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if e = r.ParseForm(); e != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	id, e := strconv.ParseInt(r.FormValue("list"), 10, 64)
	if e != nil || id <= 0 {
		http.Error(w, "list required", 400)
		return
	}
	var brand int64
	var email, brandName string
	e = s.db.Reader.QueryRowContext(r.Context(), `SELECT s.brand_id,s.email,b.name FROM subscribers s JOIN brands b ON b.id=s.brand_id JOIN list_subscribers ls ON ls.subscriber_id=s.id JOIN lists l ON l.id=ls.list_id AND l.brand_id=s.brand_id WHERE s.id=? AND l.id=? AND s.status='active' AND NOT EXISTS(SELECT 1 FROM suppressions p WHERE (p.brand_id IS NULL OR p.brand_id=s.brand_id) AND ((p.kind='email' AND p.value=s.email) OR (p.kind='domain' AND p.value=s.domain)))`, u, id).Scan(&brand, &email, &brandName)
	if e == nil {
		service := subscribers.New(s.db)
		service.SetBaseURL(s.cfg.BaseURL)
		_, e = service.PublicSubscribe(r.Context(), subscribers.UpsertInput{BrandID: brand, Email: email, ListIDs: []int64{id}, Resubscribe: true, Consent: true})
	}
	if e != nil {
		http.Error(w, "re-subscription unavailable", 400)
		return
	}
	htmlpolicy.Set(w, htmlpolicy.Custom, "no-store")
	preferenceConfirmation(w, "Preference saved", "Your subscription preference has been saved. If confirmation is needed, check your inbox to finish subscribing.", brandName)
}

func stringFields(raw string) (map[string]string, error) {
	var fields map[string]any
	if e := json.Unmarshal([]byte(raw), &fields); e != nil {
		return nil, e
	}
	out := map[string]string{}
	for k, v := range fields {
		out[k] = fmt.Sprint(v)
	}
	return out, nil
}

func (s *Service) goodbyeEmail(ctx context.Context, c *sql.Conn, brand, subscriber, listID int64) error {
	raw, e := subscribers.NotificationSetting(ctx, c, brand, listID, "goodbye_email")
	if e == sql.ErrNoRows || raw == "" {
		return nil
	}
	if e != nil {
		return e
	}
	var message struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
		Text    string `json:"text"`
	}
	if e = json.Unmarshal([]byte(raw), &message); e != nil {
		return fmt.Errorf("invalid goodbye email configuration: %w", e)
	}
	var email, name, fields string
	if e = c.QueryRowContext(ctx, `SELECT email,name,fields FROM subscribers WHERE id=?`, subscriber).Scan(&email, &name, &fields); e != nil {
		return e
	}
	values, e := stringFields(fields)
	if e != nil {
		return e
	}
	allowed := make([]string, 0, len(values))
	for k := range values {
		allowed = append(allowed, k)
	}
	p, e := render.Prepare(render.Options{Subject: message.Subject, HTML: message.HTML, Text: message.Text, Fields: allowed})
	if e != nil {
		return e
	}
	subject, body, text, e := p.Render(render.Recipient{Email: email, Name: name, Fields: values})
	if e != nil {
		return e
	}
	payload, e := json.Marshal(map[string]any{"brand_id": brand, "subscriber_id": subscriber, "to": email, "subject": subject, "html": body, "text": text})
	if e != nil {
		return e
	}
	at := time.Now().UnixMilli()
	_, e = c.ExecContext(ctx, `INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('transactional_email',?,'pending',?,?)`, string(payload), at, at)
	return e
}
