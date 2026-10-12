package subscribers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var fieldKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

type Rule struct {
	All   []Rule `json:"all,omitempty"`
	Any   []Rule `json:"any,omitempty"`
	Field string `json:"field,omitempty"`
	Op    string `json:"op,omitempty"`
	Value any    `json:"value,omitempty"`
}

func CompileSegment(r Rule) (string, []any, error) { return compileRule(r, 0) }
func compileRule(r Rule, depth int) (string, []any, error) {
	if depth > 20 {
		return "", nil, errors.New("segment exceeds 20 nesting levels")
	}
	if len(r.All) > 0 || len(r.Any) > 0 {
		if r.Field != "" || (len(r.All) > 0 && len(r.Any) > 0) {
			return "", nil, errors.New("rule must contain one of all, any or field")
		}
		children, join := r.All, " AND "
		if len(r.Any) > 0 {
			children, join = r.Any, " OR "
		}
		if len(children) > 100 {
			return "", nil, errors.New("too many segment rules")
		}
		var p []string
		var args []any
		for _, v := range children {
			q, a, e := compileRule(v, depth+1)
			if e != nil {
				return "", nil, e
			}
			p = append(p, q)
			args = append(args, a...)
		}
		return "(" + strings.Join(p, join) + ")", args, nil
	}
	if r.Field == "list" || r.Field == "opened" || r.Field == "clicked" {
		id, e := positiveInt(r.Value)
		if e != nil {
			return "", nil, e
		}
		var q string
		switch r.Field {
		case "list":
			q = "EXISTS (SELECT 1 FROM list_subscribers ls WHERE ls.list_id = ? AND ls.subscriber_id = s.id AND ls.status = 'subscribed')"
		case "opened":
			q = "EXISTS (SELECT 1 FROM campaign_recipients cr WHERE cr.campaign_id = ? AND cr.subscriber_id = s.id AND cr.first_opened_at IS NOT NULL)"
		case "clicked":
			q = "EXISTS (SELECT 1 FROM campaign_recipients cr WHERE cr.campaign_id = ? AND cr.subscriber_id = s.id AND cr.first_clicked_at IS NOT NULL)"
		}
		if r.Op == "not_in" || r.Op == "not" {
			q = "NOT " + q
		} else if r.Op != "in" && r.Op != "eq" && r.Op != "" {
			return "", nil, errors.New("invalid membership operation")
		}
		return q, []any{id}, nil
	}
	if r.Field == "inactive" {
		n, e := positiveInt(r.Value)
		if e != nil {
			return "", nil, e
		}
		return "NOT EXISTS (SELECT 1 FROM campaign_recipients cr WHERE cr.subscriber_id = s.id AND cr.campaign_id IN (SELECT c.id FROM campaigns c WHERE c.brand_id = s.brand_id AND c.status = 'sent' ORDER BY c.id DESC LIMIT ?) AND (cr.first_opened_at IS NOT NULL OR cr.first_clicked_at IS NOT NULL))", []any{n}, nil
	}
	expr := ""
	switch r.Field {
	case "email", "name", "status", "created_at":
		expr = "s." + r.Field
	case "gdpr_consent":
		expr = "s.gdpr_consent_at"
	default:
		if strings.HasPrefix(r.Field, "fields.") {
			key := strings.TrimPrefix(r.Field, "fields.")
			if !fieldKey.MatchString(key) {
				return "", nil, errors.New("invalid custom field key")
			}
			expr = "s.fields ->> '$." + key + "'"
		}
	}
	if expr == "" {
		return "", nil, errors.New("unknown segment field")
	}
	if r.Op == "is_set" {
		return expr + " IS NOT NULL", nil, nil
	}
	if r.Op == "not_set" {
		return expr + " IS NULL", nil, nil
	}
	op := map[string]string{"eq": "=", "ne": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[r.Op]
	if op == "" {
		return "", nil, errors.New("unsupported segment operation")
	}
	switch r.Value.(type) {
	case string, float64, int, int64, bool, json.Number:
	default:
		return "", nil, errors.New("segment value must be scalar")
	}
	return "(" + expr + " " + op + " ?)", []any{r.Value}, nil
}
func positiveInt(v any) (int64, error) {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case float64:
		n = int64(x)
		if float64(n) != x {
			return 0, errors.New("ID must be integral")
		}
	case json.Number:
		var e error
		n, e = x.Int64()
		if e != nil {
			return 0, e
		}
	default:
		return 0, errors.New("ID must be numeric")
	}
	if n <= 0 {
		return 0, errors.New("ID must be positive")
	}
	return n, nil
}
func (s *Service) validateFields(ctx context.Context, brandID int64, r Rule) error {
	return s.ValidateSegments(ctx, brandID, []Rule{r})
}
func (s *Service) SegmentCount(ctx context.Context, brandID int64, r Rule) (int64, error) {
	if e := s.validateFields(ctx, brandID, r); e != nil {
		return 0, e
	}
	q, a, e := CompileSegment(r)
	if e != nil {
		return 0, e
	}
	args := append([]any{brandID}, a...)
	var count int64
	hint := ""
	if strings.HasPrefix(r.Field, "fields.") {
		var id int64
		if e = s.db.Reader.QueryRowContext(ctx, "SELECT id FROM custom_fields WHERE brand_id=? AND key=? AND is_filterable=1", brandID, strings.TrimPrefix(r.Field, "fields.")).Scan(&id); e != nil {
			return 0, e
		}
		hint = fmt.Sprintf(" INDEXED BY subscribers_custom_%d", id)
	}
	e = s.db.Reader.QueryRowContext(ctx, "SELECT count(*) FROM subscribers s"+hint+" WHERE s.brand_id=? AND "+q, args...).Scan(&count)
	return count, e
}
func (s *Service) SaveSegment(ctx context.Context, v Segment) (Segment, error) {
	if _, _, e := CompileSegment(v.Rules); e != nil {
		return v, e
	}
	if e := s.validateFields(ctx, v.BrandID, v.Rules); e != nil {
		return v, e
	}
	b, e := json.Marshal(v.Rules)
	if e != nil {
		return v, e
	}
	if v.ID == 0 {
		r, e := s.db.Writer.ExecContext(ctx, "INSERT INTO segments(brand_id,name,rules,created_at) VALUES(?,?,?,?)", v.BrandID, v.Name, string(b), nowMS())
		if e != nil {
			return v, e
		}
		v.ID, e = r.LastInsertId()
		return v, e
	}
	_, e = s.db.Writer.ExecContext(ctx, "UPDATE segments SET name=?,rules=? WHERE id=? AND brand_id=?", v.Name, string(b), v.ID, v.BrandID)
	return v, e
}

// ValidateSegment enforces brand ownership and the filterable custom-field contract.
func (s *Service) ValidateSegment(ctx context.Context, brandID int64, r Rule) error {
	return s.ValidateSegments(ctx, brandID, []Rule{r})
}
