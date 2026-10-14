package subscribers

import (
	"context"
	"errors"
	"strings"
)

// SearchFilter filters the entire scoped audience before applying cursor pagination.
type SearchFilter struct {
	Email            string
	Name             string
	ListID           int64
	Status           string
	MembershipStatus string
	Sort             string
	Direction        string
}

func subscriberSearchQuery(brandID, afterID int64, limit int, f SearchFilter) (string, []any, error) {
	if f.Status != "" && f.Status != "active" && f.Status != "bounced" && f.Status != "complained" && f.Status != "blocklisted" {
		return "", nil, errors.New("status must be active, bounced, complained or blocklisted")
	}
	if f.ListID < 0 || f.MembershipStatus != "" && f.ListID == 0 {
		return "", nil, errors.New("membership_status requires list_id")
	}
	if f.MembershipStatus != "" && f.MembershipStatus != "subscribed" && f.MembershipStatus != "unsubscribed" && f.MembershipStatus != "unconfirmed" {
		return "", nil, errors.New("membership_status must be subscribed, unsubscribed or unconfirmed")
	}
	sortColumn := "id"
	if f.Sort != "" {
		sortColumn = f.Sort
	}
	switch sortColumn {
	case "id", "email", "name", "status", "gdpr_consent_at", "created_at", "last_activity":
	default:
		return "", nil, errors.New("unsupported subscriber sort column")
	}
	direction, comparison := "ASC", ">"
	if f.Direction == "desc" {
		direction, comparison = "DESC", "<"
	} else if f.Direction != "" && f.Direction != "asc" {
		return "", nil, errors.New("sort direction must be asc or desc")
	}
	customSort := sortColumn != "id" || direction != "ASC"
	sortValue := "s." + sortColumn
	if sortColumn == "gdpr_consent_at" {
		sortValue = "coalesce(s.gdpr_consent_at, 0)"
	}
	if sortColumn == "last_activity" {
		// Match populateLastActivity, including profiles without activity records.
		sortValue = `max(s.created_at,s.updated_at,coalesce(s.marketing_unsubscribed_at,s.created_at),
coalesce((SELECT max(a.at) FROM subscriber_activity a WHERE a.subscriber_id=s.id AND a.brand_id=s.brand_id),s.created_at))`
	}
	f.Email = strings.TrimSpace(f.Email)
	f.Name = strings.TrimSpace(f.Name)
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	columns := "s.id,s.brand_id,s.email,s.name,s.fields,s.status,s.gdpr_consent_at,coalesce(s.gdpr_consent_ip,''),s.created_at,s.updated_at,s.marketing_unsubscribed_at"
	from := "subscribers s INDEXED BY subscribers_brand_id"
	if f.ListID != 0 {
		// CROSS JOIN fixes the indexed list membership as the outer loop.
		from = "list_subscribers ls CROSS JOIN subscribers s ON s.id=ls.subscriber_id"
	} else if f.Status != "" {
		from = "subscribers s INDEXED BY subscribers_brand_status_id"
	}
	if customSort && f.ListID == 0 {
		// Let SQLite choose the appropriate ordering index for a sorted audience.
		from = "subscribers s"
	}
	q := "SELECT " + columns + " FROM " + from + " WHERE s.brand_id=?"
	args := []any{brandID}
	if !customSort {
		q += " AND s.id>?"
		args = append(args, afterID)
	} else if afterID > 0 {
		// Use the last row's sort value and ID together so duplicate values never
		// skip or repeat recipients. The cursor lookup stays inside the brand.
		value := sortValue
		if sortColumn == "gdpr_consent_at" {
			// SQLite does not derive an expression-index range from the row
			// comparison below. Supply the inclusive first-key bound explicitly;
			// the tuple still handles equal consent values and the ID tie breaker.
			q += " AND " + sortValue + " " + comparison + "= (SELECT " + value + " FROM subscribers s WHERE s.brand_id=? AND s.id=?)"
			args = append(args, brandID, afterID)
		}
		q += " AND (" + sortValue + ",s.id) " + comparison + " (SELECT " + value + ",s.id FROM subscribers s WHERE s.brand_id=? AND s.id=?)"
		args = append(args, brandID, afterID)
	}
	if f.Email != "" {
		q += ` AND s.email LIKE ? ESCAPE '\'`
		args = append(args, containsPattern(f.Email))
	}
	if f.Name != "" {
		q += ` AND s.name LIKE ? ESCAPE '\'`
		args = append(args, containsPattern(f.Name))
	}
	if f.Status != "" {
		q += " AND s.status=?"
		args = append(args, f.Status)
	}
	if f.ListID != 0 {
		q += " AND ls.list_id=?"
		if !customSort {
			q += " AND ls.subscriber_id>?"
		}
		args = append(args, f.ListID)
		if !customSort {
			args = append(args, afterID)
		}
		if f.MembershipStatus != "" {
			q += " AND ls.status=?"
			args = append(args, f.MembershipStatus)
		}
	}
	if customSort {
		q += " ORDER BY " + sortValue + " " + direction + ",s.id " + direction + " LIMIT ?"
	} else if f.ListID != 0 {
		q += " ORDER BY ls.subscriber_id LIMIT ?"
	} else {
		q += " ORDER BY s.id LIMIT ?"
	}
	return q, append(args, limit), nil
}

// Treat search text as a literal substring, including SQL wildcard characters.
func containsPattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return "%" + value + "%"
}

func (s *Service) SearchFiltered(ctx context.Context, brandID, afterID int64, limit int, f SearchFilter) ([]Subscriber, error) {
	q, args, err := subscriberSearchQuery(brandID, afterID, limit, f)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscriber{}
	for rows.Next() {
		v, err := scanSubscriber(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := s.populateLastActivity(ctx, brandID, out); err != nil {
		return nil, err
	}
	return out, nil
}

const latestActivityQuery = `SELECT s.id, a.kind, a.at
FROM subscribers s JOIN subscriber_activity a ON a.id=(
 SELECT id FROM subscriber_activity WHERE subscriber_id=s.id AND brand_id=s.brand_id ORDER BY at DESC,id DESC LIMIT 1
) WHERE s.brand_id=? AND s.id IN (`

// Load activity for only the current page, without a request per subscriber.
func (s *Service) populateLastActivity(ctx context.Context, brandID int64, subscribers []Subscriber) error {
	if len(subscribers) == 0 {
		return nil
	}
	args := []any{brandID}
	placeholders := make([]string, len(subscribers))
	byID := make(map[int64]*Subscriber, len(subscribers))
	for i := range subscribers {
		v := &subscribers[i]
		v.LastActivityAt, v.LastActivityKind = v.CreatedAt, "created"
		if v.UpdatedAt > v.LastActivityAt {
			v.LastActivityAt, v.LastActivityKind = v.UpdatedAt, "updated"
		}
		if v.MarketingUnsubscribedAt != nil && *v.MarketingUnsubscribedAt >= v.LastActivityAt {
			v.LastActivityAt, v.LastActivityKind = *v.MarketingUnsubscribedAt, "unsubscribe"
		}
		byID[v.ID] = v
		placeholders[i] = "?"
		args = append(args, v.ID)
	}
	rows, err := s.db.Reader.QueryContext(ctx, latestActivityQuery+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, at int64
		var kind string
		if err := rows.Scan(&id, &kind, &at); err != nil {
			return err
		}
		if v := byID[id]; v != nil && at >= v.LastActivityAt {
			if kind == "upsert" {
				kind = "updated"
				if at == v.CreatedAt {
					kind = "created"
				}
			}
			v.LastActivityAt, v.LastActivityKind = at, kind
		}
	}
	return rows.Err()
}
