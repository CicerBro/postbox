package subscribers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"postbox/internal/csvexport"
	"postbox/internal/render"
	"strconv"
	"strings"
)

// Mapping maps CSV header names to email, name, status, or fields.KEY.
// Each destination may appear once. Mapped fields.KEY columns override the
// JSON fields object, regardless of their position in the CSV header.
type ImportOptions struct {
	BrandID   int64             `json:"brand_id"`
	ListIDs   []int64           `json:"list_ids"`
	Mapping   map[string]string `json:"mapping"`
	DryRun    bool              `json:"dry_run"`
	BatchSize int               `json:"batch_size"`
}
type ImportResult struct {
	Rows     int64         `json:"rows"`
	Imported int64         `json:"imported"`
	Invalid  int64         `json:"invalid"`
	Errors   []string      `json:"errors,omitempty"`
	Preview  []UpsertInput `json:"preview,omitempty"`
}

// ImportCSV commits each full batch independently. A later read or database
// error returns the committed count and discards the pending batch. Invalid
// records are skipped, with at most 20 errors and preview records retained.
func (s *Service) ImportCSV(ctx context.Context, reader io.Reader, opt ImportOptions) (ImportResult, error) {
	var result ImportResult
	if opt.BatchSize <= 0 || opt.BatchSize > 20000 {
		opt.BatchSize = 5000
	}
	r := csv.NewReader(reader)
	r.ReuseRecord = true
	header, e := r.Read()
	if e != nil {
		return result, e
	}
	mapping := make([]string, len(header))
	hasEmail := false
	destinations := make(map[string]bool)
	for i, h := range header {
		dest := opt.Mapping[h]
		if len(opt.Mapping) == 0 {
			dest = strings.ToLower(strings.TrimSpace(h))
			switch dest {
			case "email", "name", "status", "fields", "gdpr_consent_at":
			default:
				dest = ""
			}
		}
		if dest == "email" {
			hasEmail = true
		}
		if dest != "" && dest != "email" && dest != "name" && dest != "status" && dest != "fields" && dest != "gdpr_consent_at" && (!strings.HasPrefix(dest, "fields.") || !fieldKey.MatchString(strings.TrimPrefix(dest, "fields."))) {
			return result, fmt.Errorf("invalid mapping %q", dest)
		}
		if dest != "" {
			if destinations[dest] {
				return result, fmt.Errorf("duplicate CSV mapping %q", dest)
			}
			destinations[dest] = true
		}
		mapping[i] = dest
	}
	if !hasEmail {
		return result, errors.New("CSV mapping requires email")
	}
	defs, e := s.CustomFields(ctx, opt.BrandID)
	if e != nil {
		return result, e
	}
	fieldTypes := map[string]string{}
	for _, d := range defs {
		fieldTypes[d.Key] = d.Type
	}
	batch := make([]UpsertInput, 0, opt.BatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if opt.DryRun {
			batch = batch[:0]
			return nil
		}
		e := s.db.Immediate(ctx, func(c *sql.Conn) error {
			memberships, e := loadMembershipBatch(ctx, c, opt.BrandID, opt.ListIDs, batch)
			if e != nil {
				return e
			}
			stmt, e := c.PrepareContext(ctx, subscriberUpsertSQL)
			if e != nil {
				return e
			}
			defer stmt.Close()
			for _, in := range batch {
				if _, e := s.upsertPrepared(ctx, c, in, stmt, memberships); e != nil {
					return fmt.Errorf("import %s: %w", in.Email, e)
				}
			}
			return nil
		})
		if e == nil {
			result.Imported += int64(len(batch))
		}
		batch = batch[:0]
		return e
	}
	for {
		if e = ctx.Err(); e != nil {
			return result, e
		}
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		result.Rows++
		if err != nil {
			var parseErr *csv.ParseError
			if !errors.As(err, &parseErr) {
				return result, fmt.Errorf("read CSV row %d: %w", result.Rows+1, err)
			}
			result.Invalid++
			if len(result.Errors) < 20 {
				result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", result.Rows+1, err))
			}
			continue
		}
		in := UpsertInput{BrandID: opt.BrandID, ListIDs: opt.ListIDs, Fields: map[string]any{}, SuppressNotifications: true}
		validFields := true
		fieldError := "invalid mapped field value"
		mappedFields := map[string]any{}
		for i, v := range record {
			switch mapping[i] {
			case "email":
				in.Email = v
			case "name":
				in.Name = v
			case "status":
				in.Status = v
			case "fields":
				// Decode separately so null cannot clear the writable map, and
				// JSON never replaces values from explicit field columns.
				var fields map[string]any
				if e := json.Unmarshal([]byte(v), &fields); e != nil {
					validFields = false
					fieldError = "fields must be a JSON object or null: " + e.Error()
				} else {
					for key, value := range fields {
						in.Fields[key] = value
					}
				}
			case "gdpr_consent_at":
				if v != "" {
					n, e := strconv.ParseInt(v, 10, 64)
					if e != nil || n < 0 {
						validFields = false
					} else {
						in.ConsentAt = &n
					}
				}
			case "":
			default:
				key := strings.TrimPrefix(mapping[i], "fields.")
				var value any = v
				switch fieldTypes[key] {
				case "number":
					if v != "" {
						n, e := strconv.ParseFloat(v, 64)
						if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
							validFields = false
						} else {
							value = n
						}
					} else {
						value = nil
					}
				case "boolean":
					if v != "" {
						n, e := strconv.ParseBool(v)
						if e != nil {
							validFields = false
						} else {
							value = n
						}
					} else {
						value = nil
					}
				}
				mappedFields[key] = value
			}
		}
		for key, value := range mappedFields {
			in.Fields[key] = value
		}
		if in.Status == "unsubscribed" || in.Status == "unconfirmed" {
			in.SubscriptionStatus = in.Status
			in.Status = "active"
		}
		email, e := render.NormalizeEmail(in.Email)
		if !validFields || e != nil || (in.Status != "" && in.Status != "active" && in.Status != "bounced" && in.Status != "complained" && in.Status != "blocklisted") {
			result.Invalid++
			if len(result.Errors) < 20 {
				reason := "invalid email or status"
				if !validFields {
					reason = fieldError
				}
				result.Errors = append(result.Errors, fmt.Sprintf("row %d: %s", result.Rows+1, reason))
			}
			continue
		}
		in.Email = email
		if len(result.Preview) < 20 {
			result.Preview = append(result.Preview, in)
		}
		batch = append(batch, in)
		if len(batch) >= opt.BatchSize {
			if e = flush(); e != nil {
				return result, e
			}
		}
	}
	if e = flush(); e != nil {
		return result, e
	}
	if !opt.DryRun {
		_, e = s.db.Writer.ExecContext(ctx, "ANALYZE")
	}
	return result, e
}

// ExportCSV retains the unfiltered export used by background jobs.
func (s *Service) ExportCSV(ctx context.Context, w io.Writer, brandID, listID int64) error {
	return s.ExportCSVFiltered(ctx, w, brandID, listID, "")
}

// ExportCSVFiltered filters delivery status or subscription state before paging.
// Without a list, subscription states match any membership in the brand.
// Unsubscribed also includes brand-wide marketing opt-outs.
func (s *Service) ExportCSVFiltered(ctx context.Context, w io.Writer, brandID, listID int64, status string) error {
	switch status {
	case "", "active", "bounced", "complained", "blocklisted", "subscribed", "unsubscribed", "unconfirmed":
	default:
		return errors.New("status must be active, bounced, complained, blocklisted, subscribed, unsubscribed or unconfirmed")
	}
	if listID < 0 {
		return errors.New("list_id must be a positive integer")
	}
	if listID != 0 {
		var exists int
		if err := s.db.Reader.QueryRowContext(ctx, "SELECT 1 FROM lists WHERE brand_id=? AND id=?", brandID, listID).Scan(&exists); err != nil {
			return err
		}
	}
	writer := csvexport.NewWriter(w)
	if e := writer.Write([]string{"email", "name", "status", "fields", "created_at", "gdpr_consent_at"}); e != nil {
		return e
	}
	var after int64
	for {
		// Keep delivery suppression visible while preserving marketing opt-outs.
		effectiveStatus := "CASE WHEN status!='active' THEN status WHEN marketing_unsubscribed_at IS NOT NULL THEN 'unsubscribed' ELSE status END"
		args := []any{}
		if listID != 0 {
			effectiveStatus = "CASE WHEN status!='active' THEN status WHEN marketing_unsubscribed_at IS NOT NULL THEN 'unsubscribed' ELSE (SELECT ls.status FROM list_subscribers ls WHERE ls.list_id=? AND ls.subscriber_id=subscribers.id) END"
			args = append(args, listID)
		} else if status == "subscribed" || status == "unsubscribed" || status == "unconfirmed" {
			effectiveStatus = "CASE WHEN status!='active' THEN status ELSE ? END"
			args = append(args, status)
		}
		columns := strings.Replace(subscriberColumns, "fields,status,", "fields,"+effectiveStatus+",", 1)
		q := "SELECT " + columns + " FROM subscribers WHERE brand_id=? AND id>?"
		args = append(args, brandID, after)
		membership := "SELECT 1 FROM list_subscribers ls WHERE ls.subscriber_id=subscribers.id"
		memberArgs := []any{}
		if listID != 0 {
			membership += " AND ls.list_id=?"
			memberArgs = append(memberArgs, listID)
			q += " AND EXISTS(" + membership + ")"
			args = append(args, listID)
		}
		switch status {
		case "subscribed", "unconfirmed":
			q += " AND status='active' AND marketing_unsubscribed_at IS NULL AND EXISTS(" + membership + " AND ls.status=?)"
			args = append(args, memberArgs...)
			args = append(args, status)
		case "unsubscribed":
			q += " AND (marketing_unsubscribed_at IS NOT NULL OR EXISTS(" + membership + " AND ls.status='unsubscribed'))"
			args = append(args, memberArgs...)
		case "":
		default:
			q += " AND status=?"
			args = append(args, status)
		}
		q += " ORDER BY id LIMIT 1000"
		rows, e := s.db.Reader.QueryContext(ctx, q, args...)
		if e != nil {
			return e
		}
		n := 0
		for rows.Next() {
			v, e := scanSubscriber(rows)
			if e != nil {
				rows.Close()
				return e
			}

			fields, _ := json.Marshal(v.Fields)
			consent := ""
			if v.ConsentAt != nil {
				consent = fmt.Sprint(*v.ConsentAt)
			}
			if e = writer.Write([]string{v.Email, v.Name, v.Status, string(fields), fmt.Sprint(v.CreatedAt), consent}); e != nil {
				rows.Close()
				return e
			}
			after = v.ID
			n++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if n == 0 {
			break
		}
		writer.Flush()
		if e = writer.Error(); e != nil {
			return e
		}
	}
	writer.Flush()
	return writer.Error()
}

// MassCSV applies unsubscribe or GDPR erase while retaining at most 500 addresses.
func (s *Service) MassCSV(ctx context.Context, reader io.Reader, brandID, listID int64, erase bool) (int, error) {
	r := csv.NewReader(reader)
	r.ReuseRecord = true
	header, e := r.Read()
	if e != nil {
		return 0, e
	}
	emailIndex := -1
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), "email") {
			emailIndex = i
		}
	}
	if emailIndex < 0 {
		return 0, errors.New("CSV requires an email header")
	}
	total := 0
	batch := make([]string, 0, 500)
	flush := func() error {
		var n int
		var e error
		if erase {
			n, e = s.MassErase(ctx, brandID, batch)
		} else {
			n, e = s.MassUnsubscribe(ctx, brandID, listID, batch)
		}
		total += n
		batch = batch[:0]
		return e
	}
	for {
		if e = ctx.Err(); e != nil {
			return total, e
		}
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return total, e
		}
		email, e := render.NormalizeEmail(row[emailIndex])
		if e != nil {
			return total, e
		}
		batch = append(batch, email)
		if len(batch) == 500 {
			if e = flush(); e != nil {
				return total, e
			}
		}
	}
	if len(batch) > 0 {
		e = flush()
	}
	return total, e
}
