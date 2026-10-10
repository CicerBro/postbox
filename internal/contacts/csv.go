package contacts

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"postbox/internal/render"
	"strconv"
	"strings"
)

// Mapping maps CSV header names to email, name, status, or fields.KEY.
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
			stmt, e := c.PrepareContext(ctx, subscriberUpsertSQL)
			if e != nil {
				return e
			}
			defer stmt.Close()
			for _, in := range batch {
				if _, e := s.upsertPrepared(ctx, c, in, stmt); e != nil {
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
			result.Invalid++
			if len(result.Errors) < 20 {
				result.Errors = append(result.Errors, fmt.Sprintf("row %d: %v", result.Rows+1, err))
			}
			continue
		}
		in := UpsertInput{BrandID: opt.BrandID, ListIDs: opt.ListIDs, Fields: map[string]any{}, SuppressNotifications: true}
		validFields := true
		for i, v := range record {
			switch mapping[i] {
			case "email":
				in.Email = v
			case "name":
				in.Name = v
			case "status":
				in.Status = v
			case "fields":
				if e := json.Unmarshal([]byte(v), &in.Fields); e != nil {
					validFields = false
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
				in.Fields[key] = value
			}
		}
		if in.Status == "unsubscribed" || in.Status == "unconfirmed" {
			in.SubscriptionStatus = in.Status
			in.Status = "active"
		}
		email, e := render.NormalizeEmail(in.Email)
		if !validFields || e != nil || (in.Status != "" && in.Status != "active" && in.Status != "bounced" && in.Status != "complained" && in.Status != "blocklisted") {
			result.Invalid++
			if len(result.Errors) < 20 {
				result.Errors = append(result.Errors, fmt.Sprintf("row %d: invalid email or status", result.Rows+1))
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
func (s *Service) ExportCSV(ctx context.Context, w io.Writer, brandID, listID int64) error {
	writer := csv.NewWriter(w)
	if e := writer.Write([]string{"email", "name", "status", "fields", "created_at", "gdpr_consent_at"}); e != nil {
		return e
	}
	var after int64
	for {
		columns := subscriberColumns
		args := []any{brandID, after}
		if listID != 0 {
			columns = strings.Replace(columns, "fields,status,", "fields,CASE WHEN status='active' THEN (SELECT ls.status FROM list_subscribers ls WHERE ls.list_id=? AND ls.subscriber_id=subscribers.id) ELSE status END,", 1)
			args = append([]any{listID}, args...)
		}
		q := "SELECT " + columns + " FROM subscribers WHERE brand_id=? AND id>?"
		if listID != 0 {
			q += " AND EXISTS(SELECT 1 FROM list_subscribers ls WHERE ls.list_id=? AND ls.subscriber_id=subscribers.id)"
			args = append(args, listID)
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
