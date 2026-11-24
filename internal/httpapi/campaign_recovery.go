package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

func (a *API) linkCampaignRecovery(w http.ResponseWriter, r *http.Request) {
	brand, id, ok := a.campaignScope(w, r)
	if !ok {
		return
	}
	var input struct {
		OriginalID int64 `json:"original_campaign_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.OriginalID <= 0 || input.OriginalID == id {
		fail(w, http.StatusBadRequest, errors.New("choose a different original campaign"))
		return
	}

	// Validate and link in one statement so concurrent requests cannot
	// create a chain, overwrite a link, or cross a brand boundary.
	res, err := a.db.Writer.ExecContext(r.Context(), `UPDATE campaigns AS recovery
		SET recovery_of_campaign_id=?
		WHERE recovery.id=? AND recovery.brand_id=? AND recovery.status='sent'
		AND (recovery.recovery_of_campaign_id IS NULL OR recovery.recovery_of_campaign_id=?)
		AND NOT EXISTS (SELECT 1 FROM campaigns WHERE recovery_of_campaign_id=recovery.id)
		AND EXISTS (SELECT 1 FROM campaign_preparations p WHERE p.campaign_id=recovery.id AND p.recipients_snapshotted=1)
		AND EXISTS (
			SELECT 1 FROM campaigns original JOIN campaign_preparations p ON p.campaign_id=original.id
			WHERE original.id=? AND original.brand_id=recovery.brand_id AND original.status='sent'
			AND original.recovery_of_campaign_id IS NULL AND p.recipients_snapshotted=1
		)`, input.OriginalID, id, brand, input.OriginalID, input.OriginalID)
	if err != nil {
		result(w, nil, err)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		result(w, nil, err)
		return
	}
	if n == 0 {
		fail(w, http.StatusConflict, errors.New("link two sent campaigns from the same brand with recipient history and no conflicting recovery links"))
		return
	}
	result(w, map[string]int64{"original_campaign_id": input.OriginalID}, nil)
}

type campaignRecoveryStats struct {
	Recipients      int64 `json:"recipients"`
	Sent            int64 `json:"sent"`
	Delivered       int64 `json:"delivered"`
	Bounced         int64 `json:"bounced"`
	AwaitingOutcome int64 `json:"awaiting_outcome"`
	Recovered       int64 `json:"recovered"`
	OpensUnique     int64 `json:"opens_unique"`
	ClicksUnique    int64 `json:"clicks_unique"`
	Unsubscribed    int64 `json:"unsubscribed"`
	Complained      int64 `json:"complained"`
}

func (a *API) combinedCampaignStats(ctx context.Context, id int64) (campaignRecoveryStats, error) {
	var s campaignRecoveryStats
	// Summarize recipient snapshots, leaving the per-send counters and SES
	// receipts intact. A delivery after a bounce supersedes that bounce.
	err := a.db.Reader.QueryRowContext(ctx, `WITH original AS (SELECT id,brand_id FROM campaigns WHERE id=?), recipients AS (
		SELECT r.subscriber_id, max(r.sent_at IS NOT NULL OR r.status='sent') AS sent,
			max(r.delivered_at) AS delivered_at, max(r.bounced_at) AS bounced_at,
			max(CASE WHEN r.campaign_id=(SELECT id FROM original) THEN r.bounced_at END) AS original_bounce_at,
			max(CASE WHEN r.campaign_id<>(SELECT id FROM original) THEN r.delivered_at END) AS recovery_delivered_at,
			max(r.first_opened_at IS NOT NULL) AS opened, max(r.first_clicked_at IS NOT NULL) AS clicked,
			max(r.unsubscribed_at IS NOT NULL) AS unsubscribed, max(r.complained_at IS NOT NULL) AS complained
		FROM campaign_recipients r JOIN subscribers s ON s.id=r.subscriber_id AND s.brand_id=(SELECT brand_id FROM original)
		WHERE r.campaign_id IN (
			SELECT id FROM original UNION ALL
			SELECT id FROM campaigns WHERE recovery_of_campaign_id=(SELECT id FROM original) AND brand_id=(SELECT brand_id FROM original)
		)
		GROUP BY r.subscriber_id
	)
	SELECT count(*),count(*) FILTER (WHERE sent),
		count(*) FILTER (WHERE delivered_at IS NOT NULL AND (bounced_at IS NULL OR delivered_at>bounced_at)),
		count(*) FILTER (WHERE bounced_at IS NOT NULL AND (delivered_at IS NULL OR bounced_at>=delivered_at)),
		count(*) FILTER (WHERE sent AND delivered_at IS NULL AND bounced_at IS NULL),
		count(*) FILTER (WHERE recovery_delivered_at>original_bounce_at AND delivered_at>bounced_at),
		count(*) FILTER (WHERE opened),count(*) FILTER (WHERE clicked),
		count(*) FILTER (WHERE unsubscribed),count(*) FILTER (WHERE complained)
	FROM recipients`, id).Scan(&s.Recipients, &s.Sent, &s.Delivered, &s.Bounced, &s.AwaitingOutcome,
		&s.Recovered, &s.OpensUnique, &s.ClicksUnique, &s.Unsubscribed, &s.Complained)
	return s, err
}

func (a *API) campaignRecoveryData(ctx context.Context, brand, id int64) (map[string]any, error) {
	var parent sql.NullInt64
	if err := a.db.Reader.QueryRowContext(ctx, "SELECT recovery_of_campaign_id FROM campaigns WHERE id=? AND brand_id=?", id, brand).Scan(&parent); err != nil {
		return nil, err
	}
	if parent.Valid {
		return map[string]any{"original_campaign_id": parent.Int64}, nil
	}
	sends, err := queryRows(ctx, a.db.Reader, `SELECT c.id,c.subject,c.finished_at,
		coalesce(s.sent,0) AS sent,coalesce(s.delivered,0) AS delivered
		FROM campaigns c LEFT JOIN campaign_stats s ON s.campaign_id=c.id
		WHERE c.brand_id=? AND (c.id=? OR c.recovery_of_campaign_id=?)
		ORDER BY c.id<>?,c.id`, brand, id, id, id)
	if err != nil || len(sends) < 2 {
		return nil, err
	}
	stats, err := a.combinedCampaignStats(ctx, id)
	return map[string]any{"original_campaign_id": id, "sends": sends, "stats": stats}, err
}
