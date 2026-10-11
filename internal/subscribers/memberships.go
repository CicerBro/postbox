package subscribers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"postbox/internal/jobs"
	"strings"
)

// membershipBatch is transaction-local. Configuration and memberships are loaded
// once before an import batch, and writes update the cache for duplicate emails.
type membershipBatch struct {
	optIn    map[int64]string
	previous map[string]map[int64]string
}

func (b *membershipBatch) set(email string, list int64, status string) {
	if b.previous[email] == nil {
		b.previous[email] = map[int64]string{}
	}
	b.previous[email][list] = status
}

func membershipOptIn(ctx context.Context, c *sql.Conn, batch *membershipBatch, list, brand int64) (string, error) {
	if batch != nil {
		opt, ok := batch.optIn[list]
		if !ok {
			return "", sql.ErrNoRows
		}
		return opt, nil
	}
	var opt string
	err := c.QueryRowContext(ctx, "SELECT opt_in FROM lists WHERE id=? AND brand_id=?", list, brand).Scan(&opt)
	return opt, err
}

func membershipPrevious(ctx context.Context, c *sql.Conn, batch *membershipBatch, list int64, v Subscriber) (string, error) {
	if batch != nil {
		return batch.previous[v.Email][list], nil
	}
	var previous string
	err := c.QueryRowContext(ctx, "SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", list, v.ID).Scan(&previous)
	if err == sql.ErrNoRows {
		err = nil
	}
	return previous, err
}

func loadMembershipBatch(ctx context.Context, c *sql.Conn, brand int64, listIDs []int64, batch []UpsertInput) (*membershipBatch, error) {
	loaded := &membershipBatch{optIn: map[int64]string{}, previous: map[string]map[int64]string{}}
	if len(listIDs) == 0 {
		return loaded, nil
	}
	// Query only selected lists and reject foreign or nonexistent lists before writes.
	args := []any{brand}
	placeholders := make([]string, len(listIDs))
	for i, id := range listIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	rows, err := c.QueryContext(ctx, "SELECT id,opt_in FROM lists WHERE brand_id=? AND id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var opt string
		if err = rows.Scan(&id, &opt); err != nil {
			rows.Close()
			return nil, err
		}
		loaded.optIn[id] = opt
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range listIDs {
		if _, ok := loaded.optIn[id]; !ok {
			return nil, fmt.Errorf("list %d does not belong to brand", id)
		}
	}
	// Bounded IN queries keep database parameter counts and memory independent of
	// total file size, including when the import uses its maximum batch size.
	for start := 0; start < len(batch); start += 500 {
		end := min(start+500, len(batch))
		args = []any{brand}
		placeholders = make([]string, end-start)
		for i, in := range batch[start:end] {
			placeholders[i] = "?"
			args = append(args, in.Email)
		}
		listPlaceholders := make([]string, len(listIDs))
		for i, id := range listIDs {
			listPlaceholders[i] = "?"
			args = append(args, id)
		}
		rows, err = c.QueryContext(ctx, "SELECT s.email,ls.list_id,ls.status FROM subscribers s JOIN list_subscribers ls ON ls.subscriber_id=s.id WHERE s.brand_id=? AND s.email IN ("+strings.Join(placeholders, ",")+") AND ls.list_id IN ("+strings.Join(listPlaceholders, ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var email, status string
			var list int64
			if err = rows.Scan(&email, &list, &status); err != nil {
				rows.Close()
				return nil, err
			}
			if _, ok := loaded.optIn[list]; ok {
				loaded.set(email, list, status)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

func (s *Service) applyMemberships(ctx context.Context, c *sql.Conn, in UpsertInput, v Subscriber, at int64, memberships *membershipBatch) (Subscriber, error) {
	var e error
	v.ConfirmationTokens = map[int64]string{}
	// Renewing brand consent requires an explicit list subscription request and
	// cannot clear a delivery suppression or bypass a double opt-in list.
	if v.MarketingUnsubscribedAt != nil && in.Resubscribe && !in.Consent && (in.ConsentAt == nil || *in.ConsentAt <= *v.MarketingUnsubscribedAt) {
		return v, errors.New("brand resubscription requires fresh consent")
	}
	canResubscribe := false
	if in.Resubscribe && len(in.ListIDs) > 0 && v.Status == "active" {
		var blocked bool
		if e = c.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM suppressions p JOIN subscribers s ON s.id=? WHERE (p.brand_id IS NULL OR p.brand_id=s.brand_id) AND ((p.kind='email' AND p.value=s.email) OR (p.kind='domain' AND p.value=s.domain)))`, v.ID).Scan(&blocked); e != nil {
			return v, e
		}
		canResubscribe = !blocked
	}
	var removedList int64
	for _, id := range in.ListIDs {
		opt, err := membershipOptIn(ctx, c, memberships, id, in.BrandID)
		if err != nil {
			e = err
			return v, fmt.Errorf("list %d does not belong to brand: %w", id, e)
		}
		previous, err := membershipPrevious(ctx, c, memberships, id, v)
		if err != nil {
			e = err
			return v, e
		}
		next, change := membershipTransition(in, v, opt, previous, canResubscribe)
		if !change {
			continue
		}
		if canResubscribe && next == "subscribed" && (v.MarketingUnsubscribedAt != nil || previous == "unsubscribed") {
			if _, e = c.ExecContext(ctx, `UPDATE subscribers SET marketing_unsubscribed_at=NULL,gdpr_consent_at=?,gdpr_consent_ip=? WHERE id=? AND brand_id=?`, at, in.ConsentIP, v.ID, in.BrandID); e != nil {
				return v, e
			}
			v.MarketingUnsubscribedAt = nil
			v.ConsentAt = &at
			v.ConsentIP = in.ConsentIP
		}
		var confirmationStarted any
		if next == "unconfirmed" {
			confirmationStarted = at
		}
		_, e = c.ExecContext(ctx, "INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at,confirmation_started_at) VALUES(?,?,?,?,?,?) ON CONFLICT(list_id,subscriber_id) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at,confirmation_started_at=excluded.confirmation_started_at", id, v.ID, next, at, at, confirmationStarted)
		if e != nil {
			return v, e
		}
		if previous == "unconfirmed" && next != "unconfirmed" {
			if _, e = c.ExecContext(ctx, "DELETE FROM subscription_confirmations WHERE list_id=? AND subscriber_id=?", id, v.ID); e != nil {
				return v, e
			}
		}
		if memberships != nil {
			memberships.set(v.Email, id, next)
		}
		if previous == "subscribed" && next != "subscribed" {
			if e = membershipCounter(ctx, c, id, -1, 0, 1, at); e != nil {
				return v, e
			}
			removedList = id
		}
		if next == "subscribed" && previous != next {
			if e = membershipCounter(ctx, c, id, 1, 1, 0, at); e != nil {
				return v, e
			}
		}
		if previous != next {
			event := "unsubscribe"
			if next == "subscribed" {
				event = "subscribe"
			}
			if next == "subscribed" || (previous != "" && next == "unsubscribed") || previous == "subscribed" {
				if e = jobs.EmitTx(ctx, c, in.BrandID, event, map[string]any{"subscriber_id": v.ID, "list_id": id}); e != nil {
					return v, e
				}
			}
			if next == "subscribed" && !in.SuppressNotifications {
				if e = EnqueueThankyouTx(ctx, c, in.BrandID, id, v.ID); e != nil {
					return v, e
				}
			}
		}
		if next == "unconfirmed" {
			token, e := CreateConfirmationTx(ctx, c, id, v.ID)
			if e != nil {
				return v, e
			}
			if canResubscribe && v.MarketingUnsubscribedAt != nil {
				if _, e = c.ExecContext(ctx, `UPDATE subscription_confirmations SET resubscribe_brand=1 WHERE list_id=? AND subscriber_id=?`, id, v.ID); e != nil {
					return v, e
				}
			}
			v.ConfirmationTokens[id] = token
			if s.baseURL != "" && !in.SuppressNotifications {
				if e := EnqueueConfirmationTx(ctx, c, in.BrandID, id, v.ID, token, s.baseURL); e != nil {
					return v, e
				}
			}
		}

	}
	if removedList > 0 {
		if e = SkipAfterUnsubscribeTx(ctx, c, v.ID, removedList); e != nil {
			return v, e
		}
	}
	if v.Status != "active" {
		if e = unsubscribeMemberships(ctx, c, in.BrandID, v.ID, 0, false); e != nil {
			return v, e
		}
	}
	_, e = c.ExecContext(ctx, "INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(?,?,'upsert',?,'{}')", in.BrandID, v.ID, at)
	return v, e
}

// membershipTransition applies the same consent policy to public requests,
// authenticated upserts and imports before any database state is changed.
func membershipTransition(in UpsertInput, v Subscriber, opt, previous string, canResubscribe bool) (string, bool) {
	next := "subscribed"
	if opt == "double" {
		next = "unconfirmed"
	}
	if in.SubscriptionStatus != "" {
		next = in.SubscriptionStatus
	}
	if in.Resubscribe && opt == "double" && next == "subscribed" {
		next = "unconfirmed"
	}
	if v.Status != "active" || (v.MarketingUnsubscribedAt != nil && !canResubscribe) {
		next = "unsubscribed"
	}
	if previous == "unsubscribed" && !canResubscribe {
		return next, false
	}
	if previous == "subscribed" && v.Status == "active" && (in.SubscriptionStatus == "" || next == "subscribed") {
		return next, false
	}
	return next, true
}
