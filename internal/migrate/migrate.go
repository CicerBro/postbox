package migrate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"postbox/internal/render"
	"postbox/internal/store"
)

type Options struct {
	DSN       string `json:"-"`
	DumpPath  string `json:"dump_path,omitempty"`
	SourceID  string `json:"source_id"`
	DryRun    bool   `json:"dry_run"`
	BatchSize int    `json:"batch_size"`
}
type Report struct {
	SourceID      string               `json:"source_id"`
	DryRun        bool                 `json:"dry_run"`
	Rows          map[string]int64     `json:"rows"`
	Imported      map[string]int64     `json:"imported"`
	Statuses      map[string]int64     `json:"statuses"`
	Deduplicated  int64                `json:"deduplicated"`
	Suppressed    int64                `json:"suppressed"`
	Warnings      []string             `json:"warnings"`
	StatsRecovery *StatsRecoveryReport `json:"stats_recovery,omitempty"`
}

func Run(ctx context.Context, db *store.DB, opt Options) (Report, error) {
	if (opt.DSN == "") == (opt.DumpPath == "") {
		return Report{}, errors.New("provide exactly one Sendy MySQL DSN or SQL dump path")
	}
	if opt.DSN != "" {
		return Import(ctx, db, MySQLSource{DSN: opt.DSN}, opt)
	}
	f, err := os.Open(opt.DumpPath)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	return Import(ctx, db, DumpSource{Reader: f}, opt)
}

// Import spools rows to private temporary files to resolve dependencies without
// retaining subscriber data in memory. Each batch is independently durable.
// Repeat a failed import with the same SourceID to resume idempotently.
func Import(ctx context.Context, db *store.DB, source Source, opt Options) (report Report, err error) {
	report = Report{SourceID: opt.SourceID, DryRun: opt.DryRun, Rows: map[string]int64{}, Imported: map[string]int64{}, Statuses: map[string]int64{}}
	if opt.SourceID == "" {
		return report, errors.New("source_id is required and must stay stable across reruns")
	}
	if opt.BatchSize <= 0 {
		opt.BatchSize = 1000
	}
	if opt.BatchSize > 10000 {
		return report, errors.New("batch_size must not exceed 10000")
	}
	dir, err := os.MkdirTemp("", "postbox-sendy-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	files := map[string]*os.File{}
	writers := map[string]*bufio.Writer{}
	paths := map[string]string{}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	warned := map[string]bool{}
	warn := func(s string) {
		if !warned[s] {
			warned[s] = true
			report.Warnings = append(report.Warnings, s)
		}
	}
	err = source.Scan(ctx, func(r Row) error {
		r.Table = strings.ToLower(r.Table)
		for key, value := range r.Values {
			if value != nil && !utf8.ValidString(*value) {
				return fmt.Errorf("non-UTF-8 value in %s.%s; convert source encoding before import", r.Table, key)
			}
		}
		report.Rows[r.Table]++
		if _, ok := writers[r.Table]; !ok {
			f, e := os.CreateTemp(dir, "rows-")
			if e != nil {
				return e
			}
			files[r.Table] = f
			paths[r.Table] = f.Name()
			writers[r.Table] = bufio.NewWriterSize(f, 64*1024)
		}
		b, e := json.Marshal(r)
		if e != nil {
			return e
		}
		if _, e = writers[r.Table].Write(append(b, '\n')); e != nil {
			return e
		}
		known, ok := knownColumns[r.Table]
		if !ok {
			warn("table " + r.Table + " is archived only; no application mapping is defined")
		}
		for k := range r.Values {
			if ok && !known[k] {
				warn("column " + r.Table + "." + k + " is preserved in migration_records but has no application mapping")
			}
		}
		return nil
	})
	if err != nil {
		return report, fmt.Errorf("read Sendy source: %w", err)
	}
	for name, w := range writers {
		if err = w.Flush(); err != nil {
			return report, err
		}
		if err = files[name].Close(); err != nil {
			return report, err
		}
	}
	for _, table := range []string{"apps"} {
		if report.Rows[table] == 0 {
			return report, fmt.Errorf("source has no %s rows; expected an unprefixed Sendy database", table)
		}
	}
	if opt.DryRun {
		temp, e := store.Open(filepath.Join(dir, "preview.sqlite"))
		if e != nil {
			return report, e
		}
		defer temp.Close()
		db = temp
	}
	if db == nil {
		return report, errors.New("destination database is required")
	}
	// Disable sending during migration, including partially completed imports.
	if !opt.DryRun {
		if err = db.SetSetting(ctx, "sending_enabled", "false"); err != nil {
			return report, err
		}
		warn("sending is disabled; review the import and enable sending explicitly afterwards")
	}
	order := []string{"apps", "lists", "custom_fields", "customfields", "suppressions", "suppression_list", "suppression_lists", "blocked_domains", "subscribers", "templates", "campaigns"}
	for table := range paths {
		found := false
		for _, t := range order {
			if t == table {
				found = true
			}
		}
		if !found {
			order = append(order, table)
		}
	}
	im := importer{ctx: ctx, source: opt.SourceID, report: &report, warn: warn, now: time.Now().UnixMilli(), newCampaigns: map[int64]bool{}}
	for _, table := range order {
		im.ordinal = 0
		path, ok := paths[table]
		if !ok {
			continue
		}
		f, e := os.Open(path)
		if e != nil {
			return report, e
		}
		dec := json.NewDecoder(bufio.NewReaderSize(f, 64*1024))
		done := false
		for !done {
			batch := make([]Row, 0, opt.BatchSize)
			for len(batch) < opt.BatchSize {
				var r Row
				e = dec.Decode(&r)
				if errors.Is(e, io.EOF) {
					done = true
					break
				}
				if e != nil {
					f.Close()
					return report, e
				}
				batch = append(batch, r)
			}
			if len(batch) == 0 {
				break
			}
			beforeImported, beforeStatuses := maps.Clone(report.Imported), maps.Clone(report.Statuses)
			beforeDedup, beforeSuppressed := report.Deduplicated, report.Suppressed
			e = db.Immediate(ctx, func(c *sql.Conn) error {
				im.conn = c
				im.statements = map[string]*sql.Stmt{}
				defer func() {
					for _, stmt := range im.statements {
						stmt.Close()
					}
				}()
				for _, r := range batch {
					if e := im.row(r); e != nil {
						return fmt.Errorf("%s source id %s: %w", r.Table, r.get("id"), e)
					}
				}
				return nil
			})
			if e != nil {
				report.Imported, report.Statuses = beforeImported, beforeStatuses
				report.Deduplicated, report.Suppressed = beforeDedup, beforeSuppressed
				f.Close()
				return report, e
			}
		}
		f.Close()
	}
	if len(im.newCampaigns) > 0 {
		recovered, e := recoverCampaignStats(ctx, db, opt.SourceID, false, im.newCampaigns)
		if e != nil {
			return report, e
		}
		recovered.DryRun = opt.DryRun
		report.StatsRecovery = &recovered
	}
	if err = db.Analyze(ctx); err != nil {
		return report, err
	}
	sort.Strings(report.Warnings)
	return report, nil
}

type importer struct {
	ctx          context.Context
	conn         *sql.Conn
	source       string
	report       *Report
	warn         func(string)
	now          int64
	ordinal      int64
	statements   map[string]*sql.Stmt
	newCampaigns map[int64]bool
}

func (i *importer) statement(q string) (*sql.Stmt, error) {
	if s := i.statements[q]; s != nil {
		return s, nil
	}
	s, e := i.conn.PrepareContext(i.ctx, q)
	if e == nil {
		i.statements[q] = s
	}
	return s, e
}
func (i *importer) query(q string, args ...any) *sql.Row {
	if s, e := i.statement(q); e == nil {
		return s.QueryRowContext(i.ctx, args...)
	}
	return i.conn.QueryRowContext(i.ctx, q, args...)
}
func (r Row) get(keys ...string) string {
	for _, k := range keys {
		if v := r.Values[k]; v != nil {
			return *v
		}
	}
	return ""
}
func (r Row) has(k string) bool { _, ok := r.Values[k]; return ok }
func (i *importer) exec(q string, args ...any) error {
	stmt, e := i.statement(q)
	if e != nil {
		return e
	}
	_, e = stmt.ExecContext(i.ctx, args...)
	return e
}
func (i *importer) mapping(entity, id string) (int64, error) {
	if id == "" {
		return 0, fmt.Errorf("missing %s identity", entity)
	}
	var n int64
	e := i.query("SELECT target_id FROM migration_identities WHERE source=? AND entity=? AND source_id=?", i.source, entity, id).Scan(&n)
	return n, e
}
func (i *importer) save(entity, id string, n int64) error {
	return i.exec("INSERT INTO migration_identities(source,entity,source_id,target_id) VALUES(?,?,?,?) ON CONFLICT(source,entity,source_id) DO UPDATE SET target_id=excluded.target_id", i.source, entity, id, n)
}
func (i *importer) insert(q string, args ...any) (int64, error) {
	stmt, e := i.statement(q)
	if e != nil {
		return 0, e
	}
	r, e := stmt.ExecContext(i.ctx, args...)
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func (i *importer) uuid(entity, id string) string {
	h := sha256.Sum256([]byte(i.source + "\x00" + entity + "\x00" + id))
	return fmt.Sprintf("sendy-%x", h[:16])
}
func (i *importer) row(r Row) error {
	id := r.get("id")
	i.ordinal++
	if id == "" {
		if _, ok := knownColumns[r.Table]; ok {
			return errors.New("missing primary key id")
		}
		i.warn("table " + r.Table + " has no id column; archived identities are table-relative row ordinals and may change on rerun")
		id = fmt.Sprintf("row:%d", i.ordinal)
	}
	b, e := json.Marshal(r.Values)
	if e != nil {
		return e
	}
	if e = i.exec("INSERT INTO migration_records(source,entity,source_id,payload) VALUES(?,?,?,?) ON CONFLICT(source,entity,source_id) DO UPDATE SET payload=excluded.payload", i.source, r.Table, id, string(b)); e != nil {
		return e
	}
	var target int64
	target, e = i.mapping(r.Table, id)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	// Source records are refreshed, but migrated identities are never overwritten:
	// local changes and unsubscribes made after migration are authoritative.
	if e == nil && r.Table != "subscribers" && !isSuppressionTable(r.Table) {
		if r.Table == "lists" {
			if e = i.listBaseline(target); e != nil {
				return e
			}
		}
		i.report.Imported[r.Table]++
		return nil
	}
	switch r.Table {
	case "apps":
		email, e := render.NormalizeEmail(r.get("from_email", "email"))
		if e != nil {
			return fmt.Errorf("invalid brand from_email: %w", e)
		}
		target, e = i.insert("INSERT INTO brands(name,from_name,from_email,reply_to,created_at) VALUES(?,?,?,?,?)", r.get("app_name", "name"), r.get("from_name"), email, r.get("reply_to"), i.date(r, "created_at", "timestamp"))
		if e != nil {
			return e
		}
	case "lists":
		brand, e := i.mapping("apps", r.get("app", "app_id", "brand_id"))
		if e != nil {
			return fmt.Errorf("list brand: %w", e)
		}
		optin := "single"
		if truth(r.get("opt_in", "double_opt_in")) {
			optin = "double"
		}
		target, e = i.insert("INSERT INTO lists(brand_id,uuid,name,opt_in,created_at) VALUES(?,?,?,?,?)", brand, i.uuid("lists", id), r.get("name"), optin, i.date(r, "created_at", "timestamp"))
		if e != nil {
			return e
		}
		if e = i.listBaseline(target); e != nil {
			return e
		}
		for _, f := range fieldDefinitions(r.get("custom_fields")) {
			if e = i.field(brand, f[0], f[1]); e != nil {
				return e
			}
		}
	case "custom_fields", "customfields":
		brand, e := i.mapping("apps", r.get("app", "app_id", "brand_id"))
		if e != nil {
			list, e2 := i.mapping("lists", r.get("list", "list_id"))
			if e2 != nil {
				return e
			}
			if e = i.query("SELECT brand_id FROM lists WHERE id=?", list).Scan(&brand); e != nil {
				return e
			}
		}
		key := r.get("key", "name", "label")
		if e = i.field(brand, key, r.get("type")); e != nil {
			return e
		}
		if e = i.query("SELECT id FROM custom_fields WHERE brand_id=? AND key=?", brand, key).Scan(&target); e != nil {
			return e
		}
	case "suppressions", "suppression_list", "suppression_lists", "blocked_domains":
		value := r.get("value", "email", "domain")
		kind := r.get("kind")
		var suppressionBrand *int64
		if r.Table == "blocked_domains" {
			kind = "domain"
			value = strings.TrimSpace(r.get("domain"))
			if app := r.get("app"); app != "" && app != "0" {
				brand, err := i.mapping("apps", app)
				if err != nil {
					return fmt.Errorf("blocked domain brand: %w", err)
				}
				suppressionBrand = &brand
			}
		}
		if kind == "" {
			kind = "email"
			if r.get("domain") != "" {
				kind = "domain"
			}
		}
		reason := r.get("reason")
		switch reason {
		case "hard_bounce", "soft_bounce_limit", "complaint", "manual", "ses_sync":
		default:
			reason = "manual"
		}
		if kind == "email" {
			var err error
			value, err = render.NormalizeEmail(value)
			if err != nil {
				return err
			}
		} else if kind == "domain" {
			var err error
			value, err = render.NormalizeEmail("postbox@" + value)
			if err != nil {
				return err
			}
			value = strings.SplitN(value, "@", 2)[1]
		} else {
			return fmt.Errorf("unsupported suppression kind %q", kind)
		}
		if e = i.exec("INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING", suppressionBrand, kind, value, reason, i.now); e != nil {
			return e
		}
		if e = i.query("SELECT id FROM suppressions WHERE brand_id IS ? AND kind=? AND value=?", suppressionBrand, kind, value).Scan(&target); e != nil {
			return e
		}
		if e = i.skipBrandSuppressed(kind, value, suppressionBrand); e != nil {
			return e
		}
		i.report.Suppressed++
	case "subscribers":
		return i.subscriber(r, id)
	case "templates":
		brand, e := i.mapping("apps", r.get("app", "app_id", "brand_id"))
		if e != nil {
			return e
		}
		target, e = i.insert("INSERT INTO templates(brand_id,name,html,created_at,updated_at) VALUES(?,?,?,?,?)", brand, r.get("name", "title"), r.get("html", "html_text"), i.date(r, "created_at", "timestamp"), i.now)
		if e != nil {
			return e
		}
	case "campaigns":
		brand, e := i.mapping("apps", r.get("app", "app_id", "brand_id"))
		if e != nil {
			return e
		}
		// Imported campaigns are historical records, never work for the send engine.
		// Archive every source campaign as finished, including unsent source drafts.
		target, e = i.insert("INSERT INTO campaigns(brand_id,uuid,status,subject,html,text,inline_css,created_at,updated_at,finished_at) VALUES(?,?,'sent',?,?,?,0,?,?,?)", brand, i.uuid("campaigns", id), r.get("subject", "title"), r.get("html_text", "html"), r.get("plain_text", "text"), i.date(r, "timestamp", "created_at"), i.now, i.now)
		if e != nil {
			return e
		}
		sent := i.number(r, "recipients", "sent_count", "sent_to", "to_send")
		opens, uniqueOpens, _, e := campaignOpens(r)
		if e != nil {
			return e
		}
		if e = i.exec("INSERT INTO campaign_stats(campaign_id,recipients,sent,opens_total,opens_unique,clicks_total,clicks_unique,unsubscribes,bounces_hard,complaints) VALUES(?,?,?,?,?,?,?,?,?,?)", target, sent, sent, opens, uniqueOpens, i.number(r, "clicks", "clicks_total"), i.number(r, "unique_clicks", "clicks_unique"), i.number(r, "unsubscribes"), i.number(r, "bounces"), i.number(r, "complaints")); e != nil {
			return e
		}
		i.newCampaigns[target] = true
		i.warn("historical campaign report source rows are preserved; recipient-level delivery and human/machine classification cannot be reconstructed from aggregate counters")
	default:
		if isSuppressionTable(r.Table) {
			return fmt.Errorf("unsupported suppression table %s; supply an explicit supported mapping before enabling sending", r.Table)
		}
		return nil
	}
	if e = i.save(r.Table, id, target); e != nil {
		return e
	}
	i.report.Imported[r.Table]++
	return nil
}
func (i *importer) skipSuppressed(kind, value string) error {
	return i.skipBrandSuppressed(kind, value, nil)
}
func (i *importer) skipBrandSuppressed(kind, value string, brand *int64) error {
	if kind == "domain" {
		return i.exec("UPDATE campaign_recipients SET status='skipped',error='Sendy migration suppression' WHERE status='pending' AND subscriber_id IN (SELECT id FROM subscribers WHERE domain=? AND (? IS NULL OR brand_id=?))", value, brand, brand)
	}
	return i.exec("UPDATE campaign_recipients SET status='skipped',error='Sendy migration suppression' WHERE status='pending' AND subscriber_id IN (SELECT id FROM subscribers WHERE email=? AND (? IS NULL OR brand_id=?))", value, brand, brand)
}
func isSuppressionTable(s string) bool {
	return strings.Contains(s, "suppress") || strings.Contains(s, "blacklist") || strings.Contains(s, "blocklist") || strings.Contains(s, "blocked")
}
func flag(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s != "" && s != "0" && s != "false" && s != "no" && s != "off"
}
func truth(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "double", "double_opt_in":
		return true
	}
	return false
}
func (i *importer) number(r Row, keys ...string) int64 {
	raw := r.get(keys...)
	if raw == "" {
		return 0
	}
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 0 {
		i.warn("historical counter " + strings.Join(keys, "/") + " is not a nonnegative integer; source retained but counter not mapped")
		return 0
	}
	return n
}
func (i *importer) date(r Row, keys ...string) int64 {
	s := r.get(keys...)
	if s == "" || s == "0" {
		return i.now
	}
	if n, e := strconv.ParseInt(s, 10, 64); e == nil {
		if n < 100000000000 {
			n *= 1000
		}
		return n
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t.UnixMilli()
		}
	}
	i.warn("unrecognized timestamp preserved in source archive; import time used")
	return i.now
}
func fieldDefinitions(s string) [][2]string {
	var out [][2]string
	if s == "" {
		return out
	}
	for _, entry := range strings.Split(s, "%s%") {
		parts := strings.SplitN(entry, ":", 2)
		typ := "text"
		if len(parts) == 2 {
			typ = parts[1]
		}
		if parts[0] != "" {
			out = append(out, [2]string{parts[0], typ})
		}
	}
	return out
}
func (i *importer) field(brand int64, key, typ string) error {
	if key == "" {
		return errors.New("empty custom field key")
	}
	typ = strings.ToLower(typ)
	switch typ {
	case "text", "number", "date", "boolean", "select":
	case "":
		typ = "text"
	default:
		i.warn("unknown custom field type " + typ + " imported as text")
		typ = "text"
	}
	return i.exec("INSERT INTO custom_fields(brand_id,key,label,type) VALUES(?,?,?,?) ON CONFLICT(brand_id,key) DO NOTHING", brand, key, key, typ)
}
func (i *importer) subscriber(r Row, id string) error {
	list, e := i.mapping("lists", r.get("list", "list_id"))
	if e != nil {
		return fmt.Errorf("subscriber list: %w", e)
	}
	var brand int64
	if e = i.query("SELECT brand_id FROM lists WHERE id=?", list).Scan(&brand); e != nil {
		return e
	}
	email, e := render.NormalizeEmail(r.get("email"))
	if e != nil {
		return fmt.Errorf("invalid subscriber email: %w", e)
	}
	status, member, reason := "active", "subscribed", ""
	rawStatus := strings.ToLower(r.get("status"))
	if rawStatus != "" && rawStatus != "active" && rawStatus != "subscribed" && rawStatus != "unsubscribed" && rawStatus != "unconfirmed" && rawStatus != "bounced" && rawStatus != "complained" && rawStatus != "complaint" {
		member = "unsubscribed"
		status = "blocklisted"
		reason = "manual"
		i.warn("subscriber status column is interpreted conservatively; non-active values remain suppressed")
	}
	if !r.has("confirmed") && !r.has("status") {
		member = "unconfirmed"
		i.warn("subscriber rows without confirmed or status remain unconfirmed pending consent review")
	}
	if (r.has("confirmed") && !truth(r.get("confirmed"))) || flag(r.get("unconfirmed")) || rawStatus == "unconfirmed" {
		member = "unconfirmed"
		if rawStatus == "unconfirmed" {
			status = "active"
			reason = ""
		}
	}
	if flag(r.get("unsubscribed")) || rawStatus == "unsubscribed" {
		member = "unsubscribed"
		if rawStatus == "unsubscribed" {
			status = "active"
			reason = ""
		}
	}
	if flag(r.get("bounced")) || rawStatus == "bounced" {
		status = "bounced"
		member = "unsubscribed"
		reason = "hard_bounce"
	}
	if (flag(r.get("complaint")) || flag(r.get("complained"))) || rawStatus == "complained" || rawStatus == "complaint" {
		status = "complained"
		member = "unsubscribed"
		reason = "complaint"
	}
	if flag(r.get("blocklisted")) || flag(r.get("suppressed")) {
		status = "blocklisted"
		member = "unsubscribed"
		reason = "manual"
	}
	for key, value := range r.Values {
		if !knownColumns["subscribers"][key] && (strings.Contains(key, "suppress") || strings.Contains(key, "blacklist") || strings.Contains(key, "blocklist") || strings.Contains(key, "complaint") || strings.Contains(key, "bounced")) {
			if value != nil && flag(*value) {
				status = "blocklisted"
				member = "unsubscribed"
				reason = "manual"
				i.warn("unmapped subscriber safety column " + key + " conservatively suppressed")
			}
		}
	}
	fields := map[string]string{}
	raw := r.get("custom_fields", "fields")
	if raw != "" {
		if json.Unmarshal([]byte(raw), &fields) != nil {
			var payload string
			var listValues map[string]*string
			if e = i.query("SELECT payload FROM migration_records WHERE source=? AND entity='lists' AND source_id=?", i.source, r.get("list", "list_id")).Scan(&payload); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(payload), &listValues); e != nil {
				return e
			}
			defs := fieldDefinitions(Row{Values: listValues}.get("custom_fields"))
			values := strings.Split(raw, "%s%")
			if len(values) != len(defs) {
				return fmt.Errorf("custom field value count %d does not match list definitions %d; source preserved", len(values), len(defs))
			}
			for n, d := range defs {
				fields[d[0]] = values[n]
			}
		}
	}
	for key := range fields {
		if e = i.field(brand, key, "text"); e != nil {
			return e
		}
	}
	b, e := json.Marshal(fields)
	if e != nil {
		return e
	}
	created := i.date(r, "join_date", "timestamp", "created_at")
	var existing int64
	e = i.query("SELECT id FROM subscribers WHERE brand_id=? AND email=?", brand, email).Scan(&existing)
	if e == nil {
		i.report.Deduplicated++
		i.warn("duplicate normalized addresses are merged; existing names and custom-field values take precedence and all source rows remain archived")
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var sub int64
	var mergedStatus string
	e = i.query(`INSERT INTO subscribers(brand_id,email,name,fields,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(brand_id,email) DO UPDATE SET
 name=CASE WHEN subscribers.name='' THEN excluded.name ELSE subscribers.name END,
 fields=json_patch(excluded.fields,subscribers.fields),
 status=CASE WHEN subscribers.status='complained' OR excluded.status='complained' THEN 'complained' WHEN subscribers.status='bounced' OR excluded.status='bounced' THEN 'bounced' WHEN subscribers.status='blocklisted' OR excluded.status='blocklisted' THEN 'blocklisted' ELSE 'active' END,
 updated_at=excluded.updated_at RETURNING id,status`, brand, email, r.get("name"), string(b), status, created, i.now).Scan(&sub, &mergedStatus)
	if e != nil {
		return e
	}
	// Existing safety restrictions also apply to newly observed memberships.
	if mergedStatus != "active" {
		member = "unsubscribed"
	}
	var suppressed int
	if e = i.query("SELECT EXISTS(SELECT 1 FROM suppressions WHERE kind='email' AND value=? AND (brand_id IS NULL OR brand_id=?))", email, brand).Scan(&suppressed); e != nil {
		return e
	}
	if suppressed != 0 {
		member = "unsubscribed"
	}
	var previous string
	e = i.query("SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", list, sub).Scan(&previous)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	e = i.exec(`INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(list_id,subscriber_id) DO UPDATE SET status=CASE WHEN list_subscribers.status='unsubscribed' OR excluded.status='unsubscribed' THEN 'unsubscribed' WHEN list_subscribers.status='unconfirmed' OR excluded.status='unconfirmed' THEN 'unconfirmed' ELSE 'subscribed' END,updated_at=excluded.updated_at`, list, sub, member, created, i.now)
	if e != nil {
		return e
	}
	var next string
	if e = i.query("SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", list, sub).Scan(&next); e != nil {
		return e
	}
	if e = i.membershipBaseline(list, previous, next, reason); e != nil {
		return e
	}
	if reason != "" {
		if e = i.exec("INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(NULL,'email',?,?,?) ON CONFLICT DO NOTHING", email, reason, i.now); e != nil {
			return e
		}
		if e = i.deactivateMemberships(email, reason); e != nil {
			return e
		}
		if e = i.skipSuppressed("email", email); e != nil {
			return e
		}
		i.report.Suppressed++
	}
	i.report.Statuses[member]++
	if status != "active" {
		i.report.Statuses[status]++
	}
	i.report.Imported["subscribers"]++
	return i.save("subscribers", id, sub)
}

// listBaseline creates the same persisted counter row as subscribers.CreateList.
// An import-day active snapshot is useful even for empty and inactive lists.
func (i *importer) listBaseline(list int64) error {
	if e := i.exec("INSERT INTO list_counters(list_id) VALUES(?) ON CONFLICT DO NOTHING", list); e != nil {
		return e
	}
	return i.exec(`INSERT INTO list_daily_stats(list_id,day,active)
SELECT list_id,?,active FROM list_counters WHERE list_id=?
ON CONFLICT(list_id,day) DO UPDATE SET active=excluded.active`, i.now/86400000*86400000, list)
}

// membershipBaseline mirrors subscribers' cumulative transition counters, but does
// not claim the import was a signup/unsubscribe event today. Adjusting the prior
// snapshot excludes imported history from Rollup's daily deltas while retaining
// any real events that already occurred today. Everything commits with the row.
func (i *importer) membershipBaseline(list int64, previous, next, reason string) error {
	var active, subscribed, unsubscribed, bounced, complained int64
	if previous == next {
		return nil
	}
	if next == "subscribed" {
		active, subscribed = 1, 1
	} else if previous == "subscribed" {
		active, unsubscribed = -1, 1
		if reason == "hard_bounce" {
			bounced = 1
		}
		if reason == "complaint" {
			complained = 1
		}
	} else {
		return nil
	}
	if e := i.exec(`INSERT INTO list_counters(list_id,active,subscribed,unsubscribed,bounced,complained)
VALUES(?,max(0,?),?,?,?,?) ON CONFLICT(list_id) DO UPDATE SET
active=max(0,active+?),subscribed=subscribed+excluded.subscribed,
unsubscribed=unsubscribed+excluded.unsubscribed,bounced=bounced+excluded.bounced,
complained=complained+excluded.complained`, list, active, subscribed, unsubscribed, bounced, complained, active); e != nil {
		return e
	}
	day := i.now / 86400000 * 86400000
	if e := i.exec(`INSERT INTO list_counter_snapshots(list_id,day,subscribed,unsubscribed,bounced,complained)
SELECT ?,?,coalesce(p.subscribed,0)+?,coalesce(p.unsubscribed,0)+?,coalesce(p.bounced,0)+?,coalesce(p.complained,0)+?
FROM (SELECT 1) LEFT JOIN list_counter_snapshots p ON p.list_id=? AND p.day=(SELECT max(day) FROM list_counter_snapshots WHERE list_id=? AND day<?)
ON CONFLICT(list_id,day) DO UPDATE SET subscribed=subscribed+?,unsubscribed=unsubscribed+?,bounced=bounced+?,complained=complained+?`,
		list, day-86400000, subscribed, unsubscribed, bounced, complained, list, list, day, subscribed, unsubscribed, bounced, complained); e != nil {
		return e
	}
	// A rollup may already have recorded today's cumulative snapshot. Keep that
	// baseline consistent too, so tomorrow does not report imported growth.
	if e := i.exec(`UPDATE list_counter_snapshots SET subscribed=subscribed+?,unsubscribed=unsubscribed+?,bounced=bounced+?,complained=complained+? WHERE list_id=? AND day>=?`, subscribed, unsubscribed, bounced, complained, list, day); e != nil {
		return e
	}
	return i.listBaseline(list)
}

// Both lookups use existing email and subscriber-membership indexes. A global
// safety restriction must update memberships from earlier batches and lists that
// are absent from this source scan, not just the current source row's list.
func (i *importer) deactivateMemberships(email, reason string) error {
	rows, e := i.conn.QueryContext(i.ctx, `SELECT ls.list_id,ls.subscriber_id,ls.status
FROM subscribers s JOIN list_subscribers ls ON ls.subscriber_id=s.id
WHERE s.email=? AND ls.status!='unsubscribed'`, email)
	if e != nil {
		return e
	}
	type membership struct {
		list, subscriber int64
		status           string
	}
	var memberships []membership
	for rows.Next() {
		var m membership
		if e = rows.Scan(&m.list, &m.subscriber, &m.status); e != nil {
			rows.Close()
			return e
		}
		memberships = append(memberships, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, m := range memberships {
		if e = i.membershipBaseline(m.list, m.status, "unsubscribed", reason); e != nil {
			return e
		}
		if e = i.exec("UPDATE list_subscribers SET status='unsubscribed',updated_at=? WHERE list_id=? AND subscriber_id=?", i.now, m.list, m.subscriber); e != nil {
			return e
		}
	}
	return nil
}

var knownColumns = func() map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for table, cols := range map[string]string{
		"blocked_domains":   "id app domain timestamp block_attempts",
		"suppressions":      "id email domain kind value reason app brand_id timestamp created_at",
		"suppression_list":  "id email domain kind value reason app brand_id timestamp created_at",
		"suppression_lists": "id email domain kind value reason app brand_id timestamp created_at",
		"apps":              "id app_name name from_name from_email email reply_to created_at timestamp",
		"lists":             "id app app_id brand_id name opt_in double_opt_in custom_fields created_at timestamp",
		"custom_fields":     "id app app_id brand_id list list_id key name label type", "customfields": "id app app_id brand_id list list_id key name label type",
		"subscribers": "id list list_id email name custom_fields fields status confirmed unconfirmed unsubscribed bounced complaint complained blocklisted suppressed join_date timestamp created_at",
		"templates":   "id app app_id brand_id name title html html_text created_at timestamp",
		"campaigns":   "id app app_id brand_id subject title html_text html plain_text text sent status recipients sent_count sent_to to_send opens opens_total unique_opens opens_unique clicks clicks_total unique_clicks clicks_unique unsubscribes bounces complaints timestamp created_at",
		"links":       "id campaign_id ares_emails_id link url clicks",
	} {
		m[table] = map[string]bool{}
		for _, c := range strings.Fields(cols) {
			m[table][c] = true
		}
	}
	return m
}()
