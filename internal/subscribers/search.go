package subscribers

import (
	"context"
	"errors"
	"strings"
)

// SearchFilter filters the entire scoped audience before applying cursor pagination.
type SearchFilter struct {
	EmailPrefix      string
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
	case "id", "email", "name", "status", "gdpr_consent_at", "created_at":
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
	f.EmailPrefix = strings.ToLower(strings.TrimSpace(f.EmailPrefix))
	for _, c := range f.EmailPrefix {
		if c < 33 || c > 126 {
			return "", nil, errors.New("q must be an ASCII email prefix")
		}
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	columns := "s.id,s.brand_id,s.email,s.name,s.fields,s.status,s.gdpr_consent_at,coalesce(s.gdpr_consent_ip,''),s.created_at,s.updated_at,s.marketing_unsubscribed_at"
	from := "subscribers s INDEXED BY subscribers_brand_id"
	if f.EmailPrefix != "" {
		// Pin the binary email range index: ordering by ID must not turn a
		// selective prefix into a scan of the entire brand ID index.
		from = "subscribers s INDEXED BY subscribers_brand_email"
	} else if f.ListID != 0 {
		// CROSS JOIN fixes the indexed list membership as the outer loop.
		from = "list_subscribers ls CROSS JOIN subscribers s ON s.id=ls.subscriber_id"
	} else if f.Status != "" {
		from = "subscribers s INDEXED BY subscribers_brand_status_id"
	}
	if customSort && f.EmailPrefix == "" && f.ListID == 0 {
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
		value := strings.ReplaceAll(sortValue, "s.", "")
		if sortColumn == "gdpr_consent_at" {
			// SQLite does not derive an expression-index range from the row
			// comparison below. Supply the inclusive first-key bound explicitly;
			// the tuple still handles equal consent values and the ID tie breaker.
			q += " AND " + sortValue + " " + comparison + "= (SELECT " + value + " FROM subscribers WHERE brand_id=? AND id=?)"
			args = append(args, brandID, afterID)
		}
		q += " AND (" + sortValue + ",s.id) " + comparison + " (SELECT " + value + ",id FROM subscribers WHERE brand_id=? AND id=?)"
		args = append(args, brandID, afterID)
	}
	if f.EmailPrefix != "" {
		upper := []byte(f.EmailPrefix)
		upper[len(upper)-1]++
		q += " AND s.email>=? AND s.email<?"
		args = append(args, f.EmailPrefix, string(upper))
	}
	if f.Status != "" {
		q += " AND s.status=?"
		args = append(args, f.Status)
	}
	if f.ListID != 0 {
		if f.EmailPrefix != "" {
			q += " AND EXISTS (SELECT 1 FROM list_subscribers ls WHERE ls.list_id=? AND ls.subscriber_id=s.id"
		} else {
			q += " AND ls.list_id=?"
			if !customSort {
				q += " AND ls.subscriber_id>?"
			}
		}
		args = append(args, f.ListID)
		if f.EmailPrefix == "" && !customSort {
			args = append(args, afterID)
		}
		if f.MembershipStatus != "" {
			q += " AND ls.status=?"
			args = append(args, f.MembershipStatus)
		}
		if f.EmailPrefix != "" {
			q += ")"
		}
	}
	if customSort {
		q += " ORDER BY " + sortValue + " " + direction + ",s.id " + direction + " LIMIT ?"
	} else if f.ListID != 0 && f.EmailPrefix == "" {
		q += " ORDER BY ls.subscriber_id LIMIT ?"
	} else {
		q += " ORDER BY s.id LIMIT ?"
	}
	return q, append(args, limit), nil
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
	return out, rows.Err()
}
