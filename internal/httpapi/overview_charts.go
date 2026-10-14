package httpapi

import (
	"context"
	"time"
)

// Calendar arithmetic, rather than fixed 24-hour offsets, preserves DST boundaries.
func localDay(now time.Time, location *time.Location) time.Time {
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}

func (a *API) overviewCharts(ctx context.Context, brand int64, now time.Time, location *time.Location) ([]map[string]any, []map[string]any, []map[string]any, error) {
	today := localDay(now, location)
	growthStart := today.AddDate(0, 0, -59)
	growth := make([]map[string]any, 0, 60)
	for i := 0; i < 60; i++ {
		day := growthStart.AddDate(0, 0, i)
		end := day.AddDate(0, 0, 1)
		cutoff := end.UnixMilli() - 1
		if cutoff > now.UnixMilli() {
			cutoff = now.UnixMilli()
		}
		// Before timestamped history existed, use the last available UTC snapshot.
		// The approximation is surfaced to clients instead of claiming exact history.
		var active, approximate, subscribed, unsubscribed int64
		err := a.db.Reader.QueryRowContext(ctx, `SELECT
coalesce(sum(coalesce(
 (SELECT h.active FROM list_active_history h WHERE h.list_id=l.id AND h.at<=? ORDER BY h.at DESC LIMIT 1),
 (SELECT d.active FROM list_daily_stats d WHERE d.list_id=l.id AND d.day<=? AND min(d.day+86400000-1,?)<=? ORDER BY d.day DESC LIMIT 1),0)),0),
coalesce(sum(CASE WHEN NOT EXISTS(SELECT 1 FROM list_active_history h WHERE h.list_id=l.id AND h.at<=?)
 AND EXISTS(SELECT 1 FROM list_daily_stats d WHERE d.list_id=l.id AND d.day<=? AND min(d.day+86400000-1,?)<=?) THEN 1 ELSE 0 END),0)
FROM lists l WHERE l.brand_id=?`, cutoff, cutoff, now.UnixMilli(), cutoff, cutoff, cutoff, now.UnixMilli(), cutoff, brand).Scan(&active, &approximate)
		if err == nil {
			err = a.db.Reader.QueryRowContext(ctx, `SELECT coalesce(sum(h.subscribed),0),coalesce(sum(h.unsubscribed),0)
FROM lists l JOIN list_active_history h ON h.list_id=l.id WHERE l.brand_id=? AND h.at>=? AND h.at<=?`, brand, day.UnixMilli(), cutoff).Scan(&subscribed, &unsubscribed)
		}
		if err != nil {
			return nil, nil, nil, err
		}
		growth = append(growth, map[string]any{"day": day.UnixMilli(), "end": end.UnixMilli(), "active": active, "subscribed": subscribed, "unsubscribed": unsubscribed, "approximate": approximate > 0})
	}
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)
	rows, err := queryRows(ctx, a.db.Reader, `SELECT at,sent FROM send_activity WHERE brand_id=? AND at>=? AND at<? ORDER BY at`, brand, yesterday.UnixMilli(), tomorrow.UnixMilli())
	if err != nil {
		return nil, nil, nil, err
	}
	var current, previous [24]int64
	elapsedCounts := map[int64]int64{}
	for _, row := range rows {
		at := time.UnixMilli(row["at"].(int64)).In(location)
		if at.UnixMilli() > now.UnixMilli() {
			continue
		}
		elapsedCounts[int64(at.Sub(yesterday)/time.Hour)] += row["sent"].(int64)
		if at.Before(today) {
			previous[at.Hour()] += row["sent"].(int64)
		} else {
			current[at.Hour()] += row["sent"].(int64)
		}
	}
	// Keep 24 wall-clock slots. Repeated hours are combined; skipped hours have
	// no timestamps. Minute stepping also supports half-hour DST transitions.
	type interval struct{ start, end any }
	intervals := func(start, end time.Time) [24]interval {
		var result [24]interval
		for at := start; at.Before(end); at = at.Add(time.Minute) {
			h := at.In(location).Hour()
			if result[h].start == nil {
				result[h].start = at.UnixMilli()
			}
			result[h].end = at.Add(time.Minute).UnixMilli()
		}
		return result
	}
	currentIntervals, previousIntervals := intervals(today, tomorrow), intervals(yesterday, today)
	hours := make([]map[string]any, 0, 48)
	comparison := make([]map[string]any, 0, 24)
	for i := 0; i < 24; i++ {
		comparison = append(comparison, map[string]any{"hour": i, "today": current[i], "yesterday": previous[i], "today_start": currentIntervals[i].start, "today_end": currentIntervals[i].end, "yesterday_start": previousIntervals[i].start, "yesterday_end": previousIntervals[i].end})
	}
	for at := yesterday; at.Before(tomorrow); at = at.Add(time.Hour) {
		sent := elapsedCounts[int64(at.Sub(yesterday)/time.Hour)]
		hours = append(hours, map[string]any{"hour": at.UnixMilli(), "sent": sent})
	}
	return growth, hours, comparison, nil
}
