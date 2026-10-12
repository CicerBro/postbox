package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"postbox/internal/store"
)

type EngagementStats struct {
	OpensTotal   int64 `json:"opens_total"`
	OpensUnique  int64 `json:"opens_unique"`
	ClicksTotal  int64 `json:"clicks_total"`
	ClicksUnique int64 `json:"clicks_unique"`
}

type OutcomeStats struct {
	Delivered    int64 `json:"delivered"`
	Unsubscribes int64 `json:"unsubscribes"`
	BouncesHard  int64 `json:"bounces_hard"`
	Complaints   int64 `json:"complaints"`
}

type CampaignStatsRecovery struct {
	SourceCampaignID  string          `json:"source_campaign_id"`
	CampaignID        int64           `json:"campaign_id"`
	Before            EngagementStats `json:"before"`
	After             EngagementStats `json:"after"`
	Links             int             `json:"links"`
	StartedAt         *int64          `json:"started_at,omitempty"`
	BeforeOutcomes    OutcomeStats    `json:"before_outcomes"`
	AfterOutcomes     OutcomeStats    `json:"after_outcomes"`
	DeliveryEstimated bool            `json:"delivery_estimated"`
}

// Sendy's sent column is a Unix timestamp in the inspected production schema.
// Other adapters use sent as a boolean; do not turn those flags into dates.
func campaignSentAt(r Row) *int64 {
	n, err := strconv.ParseInt(r.get("sent"), 10, 64)
	if err != nil || n < 1000000000 {
		return nil
	}
	if n < 1000000000000 {
		n *= 1000
	}
	if n > 253402300799999 {
		return nil
	}
	return &n
}

type StatsRecoveryReport struct {
	SourceID  string                  `json:"source_id"`
	DryRun    bool                    `json:"dry_run"`
	Campaigns []CampaignStatsRecovery `json:"campaigns"`
	Warnings  []string                `json:"warnings,omitempty"`
}

// Sendy opens are subscriber_id:country entries; link clicks are subscriber IDs.
// Count source IDs, not merged Postbox contacts, to preserve Sendy's unique rates.
// The production archive also contains ID 0 for anonymous engagement. Retain it
// as one source identity rather than inventing contacts or discarding events.
func parseEngagement(raw string, countries bool) (int64, map[int64]struct{}, map[string]int64, error) {
	ids := map[int64]struct{}{}
	geo := map[string]int64{}
	var total int64
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, ids, geo, nil
	}
	for token := range strings.SplitSeq(strings.TrimSuffix(raw, ","), ",") {
		idText := strings.TrimSpace(token)
		country := ""
		if countries {
			var ok bool
			idText, country, ok = strings.Cut(idText, ":")
			if !ok {
				return 0, nil, nil, errors.New("expected subscriber_id:country opens")
			}
			for _, c := range country {
				if c < 'A' || c > 'Z' {
					return 0, nil, nil, errors.New("unsupported opens country")
				}
			}
		}
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id < 0 {
			return 0, nil, nil, errors.New("invalid engagement subscriber ID")
		}
		total++
		ids[id] = struct{}{}
		if country != "" {
			geo[country]++
		}
	}
	return total, ids, geo, nil
}

func campaignOpens(r Row) (int64, int64, map[string]int64, error) {
	raw := r.get("opens", "opens_total")
	if strings.Contains(raw, ":") || strings.Contains(raw, ",") {
		total, ids, geo, err := parseEngagement(raw, true)
		return total, int64(len(ids)), geo, err
	}
	total, err := archivedCounter(raw)
	if err != nil {
		return 0, 0, nil, err
	}
	unique, err := archivedCounter(r.get("unique_opens", "opens_unique"))
	return total, unique, nil, err
}

func archivedCounter(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("unsupported historical numeric counter")
	}
	return n, nil
}

// RecoverCampaignStats uses only archived source records. It never imports an
// audience, creates recipients/jobs, or changes sending settings or content.
// All campaigns are repaired atomically. A malformed archive aborts the repair.
func RecoverCampaignStats(ctx context.Context, db *store.DB, source string, dryRun bool) (StatsRecoveryReport, error) {
	return recoverCampaignStats(ctx, db, source, dryRun, nil)
}

func recoverCampaignStats(ctx context.Context, db *store.DB, source string, dryRun bool, only map[int64]bool) (report StatsRecoveryReport, err error) {
	report = StatsRecoveryReport{SourceID: source, DryRun: dryRun, Campaigns: []CampaignStatsRecovery{}}
	if source == "" {
		return report, errors.New("source_id is required")
	}
	if db == nil {
		return report, errors.New("destination database is required")
	}
	err = db.Immediate(ctx, func(conn *sql.Conn) error {
		rows, e := conn.QueryContext(ctx, `SELECT m.source_id,m.target_id FROM migration_identities m JOIN campaigns c ON c.id=m.target_id WHERE m.source=? AND m.entity='campaigns' ORDER BY m.source_id`, source)
		if e != nil {
			return e
		}
		type identity struct {
			sourceID string
			target   int64
		}
		var campaigns []identity
		for rows.Next() {
			var c identity
			if e = rows.Scan(&c.sourceID, &c.target); e != nil {
				rows.Close()
				return e
			}
			if only == nil || only[c.target] {
				campaigns = append(campaigns, c)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(campaigns) == 0 && only == nil {
			return errors.New("no imported campaigns found for source_id")
		}
		// Scan the source subscriber archive once, rather than once per campaign.
		outcomes, attributed, e := archivedOutcomes(ctx, conn, source)
		if e != nil {
			return e
		}
		for _, c := range campaigns {
			item, e := recoverCampaign(ctx, conn, source, c.sourceID, c.target, dryRun, outcomes[c.sourceID], attributed)
			if e != nil {
				return fmt.Errorf("campaign source id %s: %w", c.sourceID, e)
			}
			report.Campaigns = append(report.Campaigns, item)
		}
		return nil
	})
	if err != nil && !dryRun {
		report.Campaigns = nil
	}
	return report, err
}

func recoverCampaign(ctx context.Context, conn *sql.Conn, source, sourceID string, target int64, dryRun bool, outcomes OutcomeStats, available OutcomeStats) (item CampaignStatsRecovery, err error) {
	item.SourceCampaignID, item.CampaignID = sourceID, target
	var status string
	var recipients int
	err = conn.QueryRowContext(ctx, `SELECT status,(SELECT count(*) FROM campaign_recipients WHERE campaign_id=c.id) FROM campaigns c WHERE id=?`, target).Scan(&status, &recipients)
	if err != nil {
		return item, err
	}
	if (status != "sent" && status != "cancelled") || recipients != 0 {
		return item, errors.New("recovery requires a terminal historical campaign without Postbox recipients")
	}
	var payload string
	err = conn.QueryRowContext(ctx, `SELECT payload FROM migration_records WHERE source=? AND entity='campaigns' AND source_id=?`, source, sourceID).Scan(&payload)
	if err != nil {
		return item, fmt.Errorf("read archived campaign: %w", err)
	}
	r := Row{Table: "campaigns"}
	if err = json.Unmarshal([]byte(payload), &r.Values); err != nil {
		return item, err
	}
	item.StartedAt = campaignSentAt(r)
	var geo map[string]int64
	item.After.OpensTotal, item.After.OpensUnique, geo, err = campaignOpens(r)
	if err != nil {
		return item, err
	}
	item.After.ClicksTotal, err = archivedCounter(r.get("clicks", "clicks_total"))
	if err != nil {
		return item, err
	}
	item.After.ClicksUnique, err = archivedCounter(r.get("unique_clicks", "clicks_unique"))
	if err != nil {
		return item, err
	}
	err = conn.QueryRowContext(ctx, `SELECT opens_total,opens_unique,clicks_total,clicks_unique FROM campaign_stats WHERE campaign_id=?`, target).Scan(&item.Before.OpensTotal, &item.Before.OpensUnique, &item.Before.ClicksTotal, &item.Before.ClicksUnique)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	err = conn.QueryRowContext(ctx, `SELECT delivered,unsubscribes,bounces_hard,complaints FROM campaign_stats WHERE campaign_id=?`, target).Scan(&item.BeforeOutcomes.Delivered, &item.BeforeOutcomes.Unsubscribes, &item.BeforeOutcomes.BouncesHard, &item.BeforeOutcomes.Complaints)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	item.AfterOutcomes = item.BeforeOutcomes
	for _, counter := range []struct {
		keys      []string
		dest      *int64
		fallback  int64
		available bool
	}{
		{[]string{"unsubscribes"}, &item.AfterOutcomes.Unsubscribes, outcomes.Unsubscribes, available.Unsubscribes > 0},
		{[]string{"bounces"}, &item.AfterOutcomes.BouncesHard, outcomes.BouncesHard, available.BouncesHard > 0},
		{[]string{"complaints"}, &item.AfterOutcomes.Complaints, outcomes.Complaints, available.Complaints > 0},
	} {
		if raw := r.get(counter.keys...); strings.TrimSpace(raw) != "" {
			*counter.dest, err = archivedCounter(raw)
			if err != nil {
				return item, err
			}
		} else if counter.available {
			*counter.dest = counter.fallback
		}
	}
	if raw := r.get("delivered"); strings.TrimSpace(raw) != "" {
		item.AfterOutcomes.Delivered, err = archivedCounter(raw)
		if err != nil {
			return item, err
		}
	} else if available.BouncesHard > 0 && item.StartedAt != nil {
		// Match Sendy's report estimate, not SES-confirmed delivery events.
		sent, e := archivedCounter(r.get("recipients", "sent_count", "sent_to", "to_send"))
		if e != nil {
			return item, e
		}
		item.AfterOutcomes.Delivered = max(0, sent-item.AfterOutcomes.BouncesHard)
		item.DeliveryEstimated = true
	}
	// A single ID in links.clicks is a clicker, never an aggregate counter.
	rows, err := conn.QueryContext(ctx, `SELECT payload FROM migration_records WHERE source=? AND entity='links' AND CAST(json_extract(payload,'$.campaign_id') AS TEXT)=? ORDER BY source_id`, source, sourceID)
	if err != nil {
		return item, err
	}
	type linkReport struct {
		total int64
		ids   map[int64]struct{}
	}
	links := map[string]*linkReport{}
	allIDs := map[int64]struct{}{}
	var clicks int64
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			break
		}
		var link Row
		if err = json.Unmarshal([]byte(p), &link.Values); err != nil {
			break
		}
		if ares := link.get("ares_emails_id"); ares != "" && ares != "0" {
			continue
		}
		url := link.get("link", "url")
		if url == "" {
			err = errors.New("archived link has no URL")
			break
		}
		total, ids, _, e := parseEngagement(link.get("clicks"), false)
		if e != nil {
			err = e
			break
		}
		l := links[url]
		if l == nil {
			l = &linkReport{ids: map[int64]struct{}{}}
			links[url] = l
		}
		l.total += total
		clicks += total
		for id := range ids {
			l.ids[id] = struct{}{}
			allIDs[id] = struct{}{}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return item, err
	}
	if len(links) > 0 {
		item.After.ClicksTotal, item.After.ClicksUnique = clicks, int64(len(allIDs))
	}
	item.Links = len(links)
	if dryRun {
		return item, nil
	}
	if item.StartedAt != nil {
		if _, err = conn.ExecContext(ctx, "UPDATE campaigns SET started_at=? WHERE id=?", *item.StartedAt, target); err != nil {
			return item, err
		}
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO campaign_stats(campaign_id,opens_total,opens_unique,clicks_total,clicks_unique) VALUES(?,?,?,?,?) ON CONFLICT(campaign_id) DO UPDATE SET opens_total=excluded.opens_total,opens_unique=excluded.opens_unique,clicks_total=excluded.clicks_total,clicks_unique=excluded.clicks_unique`, target, item.After.OpensTotal, item.After.OpensUnique, item.After.ClicksTotal, item.After.ClicksUnique)
	if err != nil {
		return item, err
	}
	_, err = conn.ExecContext(ctx, `UPDATE campaign_stats SET delivered=?,unsubscribes=?,bounces_hard=?,complaints=? WHERE campaign_id=?`, item.AfterOutcomes.Delivered, item.AfterOutcomes.Unsubscribes, item.AfterOutcomes.BouncesHard, item.AfterOutcomes.Complaints, target)
	if err != nil {
		return item, err
	}
	urls := make([]string, 0, len(links))
	for url := range links {
		urls = append(urls, url)
	}
	sort.Strings(urls)
	for _, url := range urls {
		var id int64
		err = conn.QueryRowContext(ctx, `INSERT INTO links(campaign_id,url) VALUES(?,?) ON CONFLICT(campaign_id,url) DO UPDATE SET url=excluded.url RETURNING id`, target, url).Scan(&id)
		if err != nil {
			return item, err
		}
		l := links[url]
		_, err = conn.ExecContext(ctx, `INSERT INTO link_stats(link_id,campaign_id,clicks_total,clicks_unique) VALUES(?,?,?,?) ON CONFLICT(link_id) DO UPDATE SET clicks_total=excluded.clicks_total,clicks_unique=excluded.clicks_unique`, id, target, l.total, len(l.ids))
		if err != nil {
			return item, err
		}
	}
	for country, opens := range geo {
		_, err = conn.ExecContext(ctx, `INSERT INTO campaign_countries(campaign_id,country,opens) VALUES(?,?,?) ON CONFLICT(campaign_id,country) DO UPDATE SET opens=excluded.opens`, target, country, opens)
		if err != nil {
			return item, err
		}
	}
	return item, nil
}

// Sendy reports count source rows whose status flag is exactly one and whose
// last_campaign matches. Do not use merged contacts or current Postbox statuses.
// A last_campaign snapshot cannot reconstruct events reassigned by later sends.
func archivedOutcomes(ctx context.Context, conn *sql.Conn, source string) (map[string]OutcomeStats, OutcomeStats, error) {
	rows, err := conn.QueryContext(ctx, `SELECT trim(CAST(json_extract(payload,'$.last_campaign') AS TEXT)),
        sum(CASE WHEN trim(CAST(json_extract(payload,'$.unsubscribed') AS TEXT))='1' THEN 1 ELSE 0 END),
        sum(CASE WHEN trim(CAST(json_extract(payload,'$.bounced') AS TEXT))='1' THEN 1 ELSE 0 END),
        sum(CASE WHEN trim(CAST(json_extract(payload,'$.complaint') AS TEXT))='1' THEN 1 ELSE 0 END),
        count(json_extract(payload,'$.unsubscribed')),
        count(json_extract(payload,'$.bounced')),
        count(json_extract(payload,'$.complaint'))
        FROM migration_records WHERE source=? AND entity='subscribers'
        AND json_extract(payload,'$.last_campaign') IS NOT NULL
        AND trim(CAST(json_extract(payload,'$.last_campaign') AS TEXT)) NOT IN ('','0')
        GROUP BY trim(CAST(json_extract(payload,'$.last_campaign') AS TEXT))`, source)
	if err != nil {
		return nil, OutcomeStats{}, err
	}
	defer rows.Close()
	result := map[string]OutcomeStats{}
	var available OutcomeStats
	for rows.Next() {
		var id string
		var stats, columns OutcomeStats
		if err := rows.Scan(&id, &stats.Unsubscribes, &stats.BouncesHard, &stats.Complaints, &columns.Unsubscribes, &columns.BouncesHard, &columns.Complaints); err != nil {
			return nil, OutcomeStats{}, err
		}
		result[id] = stats
		available.Unsubscribes += columns.Unsubscribes
		available.BouncesHard += columns.BouncesHard
		available.Complaints += columns.Complaints
	}
	return result, available, rows.Err()
}
