package subscribers

import (
	"context"
	"testing"
)

func TestBatchSegmentValidationPreservesOwnershipAndFields(t *testing.T) {
	s, brand, list := fixture(t)
	ctx := context.Background()
	for _, field := range []CustomField{
		{BrandID: brand.ID, Key: "country", Type: "text", Filterable: true},
		{BrandID: brand.ID, Key: "private", Type: "text", Filterable: false},
	} {
		if _, err := s.SaveCustomField(ctx, field); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Writer.Exec(`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,'validation','sent','Hello','Hello',1,1)`, brand.ID); err != nil {
		t.Fatal(err)
	}
	rules := []Rule{
		{All: []Rule{{Field: "list", Value: list.ID}, {Field: "fields.country", Op: "eq", Value: "UK"}}},
		{Any: []Rule{{Field: "opened", Value: 1}, {Field: "clicked", Value: 1}}},
	}
	if err := s.ValidateSegments(ctx, brand.ID, rules); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateSegments(ctx, brand.ID+1, rules); err == nil {
		t.Fatal("cross-brand references accepted")
	}
	for _, rule := range []Rule{
		{Field: "fields.private", Op: "eq", Value: "secret"},
		{Field: "fields.missing", Op: "eq", Value: "unknown"},
		{Field: "opened", Value: 2},
		{All: []Rule{{Field: "list", Value: list.ID}, {Field: "list", Value: 999}}},
		{Field: "email", Op: "invalid", Value: "test@example.org"},
	} {
		if err := s.ValidateSegments(ctx, brand.ID, append(rules[:len(rules):len(rules)], rule)); err == nil {
			t.Fatalf("invalid reference accepted: %+v", rule)
		}
	}
}
