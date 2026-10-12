package send

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type audienceReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type targetKey struct {
	kind string
	id   int64
}

// ValidateAudienceTargets loads target ownership in bounded query batches.
// Callers replacing targets use their write transaction here to keep ownership
// checks atomic with the replacement.
func ValidateAudienceTargets(ctx context.Context, db audienceReader, brandID int64, targets []AudienceTarget) error {
	_, err := loadAudienceTargets(ctx, db, brandID, targets)
	return err
}

func loadAudienceTargets(ctx context.Context, db audienceReader, brandID int64, targets []AudienceTarget) (map[targetKey]string, error) {
	ids := map[string][]int64{}
	seen := map[targetKey]bool{}
	for _, target := range targets {
		if target.ID <= 0 || (target.Type != "list" && target.Type != "segment") || (target.Mode != "include" && target.Mode != "exclude") {
			return nil, errors.New("invalid audience target")
		}
		key := targetKey{target.Type, target.ID}
		if !seen[key] {
			ids[target.Type] = append(ids[target.Type], target.ID)
			seen[key] = true
		}
	}
	metadata := make(map[targetKey]string, len(seen))
	for _, kind := range []string{"list", "segment"} {
		for start := 0; start < len(ids[kind]); start += 500 {
			batch := ids[kind][start:min(start+500, len(ids[kind]))]
			args := []any{brandID}
			for _, id := range batch {
				args = append(args, id)
			}
			table, rules := "lists", "''"
			if kind == "segment" {
				table, rules = "segments", "rules"
			}
			rows, err := db.QueryContext(ctx, "SELECT id,"+rules+" FROM "+table+" WHERE brand_id=? AND id IN ("+strings.TrimRight(strings.Repeat("?,", len(batch)), ",")+")", args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id int64
				var rule string
				if err = rows.Scan(&id, &rule); err != nil {
					rows.Close()
					return nil, err
				}
				metadata[targetKey{kind, id}] = rule
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	if len(metadata) != len(seen) {
		return nil, errors.New("target belongs to another brand or does not exist")
	}
	return metadata, nil
}
