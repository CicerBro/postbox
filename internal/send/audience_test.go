package send

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

type countedAudienceReader struct {
	*sql.DB
	queries int
}

func (r *countedAudienceReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.queries++
	return r.DB.QueryContext(ctx, query, args...)
}

func TestAudienceMetadataValidationBatchesQueries(t *testing.T) {
	db, _, _ := fixture(t, 0)
	var targets []AudienceTarget
	for id := int64(1); id <= 600; id++ {
		if id > 1 {
			if _, err := db.Writer.Exec(`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(?,1,?,'Batch','single',1)`, id, fmt.Sprint(id)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Writer.Exec(`INSERT INTO segments(id,brand_id,name,rules,created_at) VALUES(?,1,'Batch','{"field":"email","op":"eq","value":"test@example.org"}',1)`, id); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, AudienceTarget{Type: "list", ID: id, Mode: "include"}, AudienceTarget{Type: "segment", ID: id, Mode: "exclude"})
	}
	reader := &countedAudienceReader{DB: db.Reader}
	metadata, err := loadAudienceTargets(context.Background(), reader, 1, targets)
	if err != nil || len(metadata) != len(targets) || reader.queries != 4 {
		t.Fatalf("metadata=%d queries=%d error=%v", len(metadata), reader.queries, err)
	}
	// Duplicate references reuse metadata rather than causing further queries.
	reader.queries = 0
	if err := ValidateAudienceTargets(context.Background(), reader, 1, append(targets[:100:100], targets[:100]...)); err != nil || reader.queries != 2 {
		t.Fatalf("duplicate targets queries=%d error=%v", reader.queries, err)
	}
	for _, invalid := range []AudienceTarget{
		{Type: "list", ID: 1, Mode: "unknown"},
		{Type: "subscriber", ID: 1, Mode: "include"},
		{Type: "list", ID: 0, Mode: "include"},
		{Type: "segment", ID: 601, Mode: "include"},
	} {
		if err := ValidateAudienceTargets(context.Background(), reader, 1, []AudienceTarget{invalid}); err == nil {
			t.Fatalf("accepted invalid target %+v", invalid)
		}
	}
	if err := ValidateAudienceTargets(context.Background(), reader, 2, targets); err == nil {
		t.Fatal("accepted another brand's targets")
	}
}
