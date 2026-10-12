package subscribers

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ValidateSegments loads referenced ownership and custom-field metadata once per
// batch rather than issuing a query for each rule in each segment.
func (s *Service) ValidateSegments(ctx context.Context, brandID int64, rules []Rule) error {
	references := map[string]map[int64]bool{"lists": {}, "campaigns": {}}
	fields := map[string]bool{}
	var collect func(Rule) error
	collect = func(rule Rule) error {
		if rule.Field == "list" || rule.Field == "opened" || rule.Field == "clicked" {
			id, err := positiveInt(rule.Value)
			if err != nil {
				return err
			}
			table := "campaigns"
			if rule.Field == "list" {
				table = "lists"
			}
			references[table][id] = true
		}
		if strings.HasPrefix(rule.Field, "fields.") {
			fields[strings.TrimPrefix(rule.Field, "fields.")] = true
		}
		for _, child := range rule.All {
			if err := collect(child); err != nil {
				return err
			}
		}
		for _, child := range rule.Any {
			if err := collect(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, rule := range rules {
		if _, _, err := CompileSegment(rule); err != nil {
			return err
		}
		if err := collect(rule); err != nil {
			return err
		}
	}
	for _, table := range []string{"lists", "campaigns"} {
		ids := make([]int64, 0, len(references[table]))
		for id := range references[table] {
			ids = append(ids, id)
		}
		for start := 0; start < len(ids); start += 500 {
			batch := ids[start:min(start+500, len(ids))]
			args := []any{brandID}
			for _, id := range batch {
				args = append(args, id)
			}
			var count int
			if err := s.db.Reader.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE brand_id=? AND id IN ("+strings.TrimRight(strings.Repeat("?,", len(batch)), ",")+")", args...).Scan(&count); err != nil {
				return err
			}
			if count != len(batch) {
				return errors.New("referenced list or campaign unavailable in brand")
			}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	rows, err := s.db.Reader.QueryContext(ctx, `SELECT key,is_filterable FROM custom_fields WHERE brand_id=?`, brandID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var filterable int
		if err := rows.Scan(&key, &filterable); err != nil {
			return err
		}
		if fields[key] {
			if filterable != 1 {
				return errors.New("custom field must be filterable")
			}
			delete(fields, key)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for key := range fields {
		return fmt.Errorf("custom field unavailable: %s", key)
	}
	return nil
}
