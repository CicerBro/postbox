package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

func (a *API) campaignRows(r *http.Request, brand, id int64) ([]map[string]any, error) {
	summary := id == 0 && r.URL.Query().Get("summary") == "true"
	projection := "c.*"
	from := "campaigns c"
	if summary {
		// Durable counters use a primary-key lookup, never a recipient scan.
		projection = "c.id,c.subject,c.status,c.created_at,c.scheduled_at,c.finished_at,c.html,s.recipients,s.sent,s.failed,s.skipped,s.unknown,s.opens_unique,s.clicks_unique"
		from += " LEFT JOIN campaign_stats s ON s.campaign_id=c.id"
	}
	after, limit := page(r)
	q := "SELECT " + projection + " FROM " + from + " WHERE c.brand_id=? AND c.id>? ORDER BY c.id LIMIT ?"
	args := []any{brand, after, limit}
	// Offset pagination supports globally sorted and filtered table views.
	// Keep the existing ID cursor contract for integrations using after.
	if id == 0 && r.URL.Query().Has("offset") {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}
		order := "CASE c.status WHEN 'draft' THEN 0 WHEN 'scheduled' THEN 1 ELSE 2 END, CASE WHEN c.status='scheduled' THEN c.scheduled_at END ASC, c.created_at DESC, c.id DESC"
		columns := map[string]string{"subject": "c.subject COLLATE NOCASE", "status": "c.status", "created_at": "c.created_at", "scheduled_at": "c.scheduled_at"}
		if column, ok := columns[r.URL.Query().Get("sort")]; ok {
			direction := "ASC"
			if r.URL.Query().Get("direction") == "desc" {
				direction = "DESC"
			}
			order = column + " " + direction + ", c.id " + direction
		}
		q = "SELECT " + projection + " FROM " + from + " WHERE c.brand_id=?"
		args = []any{brand}
		if status := r.URL.Query().Get("status"); status != "" && status != "all" {
			q += " AND c.status=?"
			args = append(args, status)
		}
		if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
			q += " AND instr(lower(c.subject), lower(?)) > 0"
			args = append(args, search)
		}
		q += " ORDER BY " + order + " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	if id != 0 {
		q = "SELECT " + projection + " FROM " + from + " WHERE c.brand_id=? AND c.id=?"
		args = []any{brand, id}
	}
	if summary {
		return a.queryCampaignSummaries(r.Context(), q, args...)
	}
	return queryRows(r.Context(), a.db.Reader, q, args...)
}

func (a *API) campaignOptions(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	after, limit := page(r)
	v, err := queryRows(r.Context(), a.db.Reader, "SELECT id,subject FROM campaigns WHERE brand_id=? AND id>? ORDER BY id LIMIT ?", brand, after, limit)
	result(w, v, err)
}

// Read one message at a time, retaining only the bounded text excerpt for the
// table. Integrations requesting the default representation keep full content.
func (a *API) queryCampaignSummaries(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := a.db.Reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, created int64
		var scheduled, finished sql.NullInt64
		var subject, status, content string
		var recipients, sent, failed, skipped, unknown, opensUnique, clicksUnique sql.NullInt64
		if err := rows.Scan(&id, &subject, &status, &created, &scheduled, &finished, &content, &recipients, &sent, &failed, &skipped, &unknown, &opensUnique, &clicksUnique); err != nil {
			return nil, err
		}
		var scheduledAt any
		if scheduled.Valid {
			scheduledAt = scheduled.Int64
		}
		var finishedAt any
		if finished.Valid {
			finishedAt = finished.Int64
		}
		var progress any
		if recipients.Valid && (status == "preparing" || status == "sending" || status == "paused") {
			progress = map[string]int64{"recipients": recipients.Int64, "sent": sent.Int64, "failed": failed.Int64, "skipped": skipped.Int64, "unknown": unknown.Int64}
		}
		var stats any
		if recipients.Valid {
			stats = map[string]int64{"recipients": recipients.Int64, "sent": sent.Int64, "opens_unique": opensUnique.Int64, "clicks_unique": clicksUnique.Int64}
		}
		out = append(out, map[string]any{"id": id, "subject": subject, "status": status, "created_at": created, "scheduled_at": scheduledAt, "finished_at": finishedAt, "excerpt": campaignExcerpt(content), "progress": progress, "stats": stats})
	}
	return out, rows.Err()
}

func campaignExcerpt(content string) string {
	tokens := html.NewTokenizer(strings.NewReader(content))
	ignored := ""
	var text strings.Builder
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return truncateExcerpt(text.String())
		case html.StartTagToken:
			name, _ := tokens.TagName()
			tag := string(name)
			if ignored == "" && (tag == "head" || tag == "style" || tag == "script") {
				ignored = tag
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			if string(name) == ignored {
				ignored = ""
			}
		case html.TextToken:
			if ignored != "" {
				continue
			}
			for word := range strings.FieldsSeq(string(tokens.Text())) {
				if text.Len() > 0 {
					text.WriteByte(' ')
				}
				// A large text token need not be copied in full for an excerpt.
				text.WriteString(truncateExcerpt(word))
				if len([]rune(text.String())) >= 110 {
					return truncateExcerpt(text.String())
				}
			}
		}
	}
}

func truncateExcerpt(text string) string {
	count := 0
	for offset := range text {
		if count == 110 {
			return text[:offset]
		}
		count++
	}
	return text
}
