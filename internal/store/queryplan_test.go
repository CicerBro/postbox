package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"postbox/internal/contacts"
	"postbox/internal/store"
)

// sourceSQL reads the expression used by production, not a SQL facsimile. Unknown
// expressions are rejected. Only explicitly supplied runtime fragments may vary.
func sourceSQL(t *testing.T, file, function, prefix string, variables map[string]string) string {
	t.Helper()
	tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for k, v := range variables {
		values[k] = v
	}
	var eval func(ast.Expr) (string, bool)
	eval = func(e ast.Expr) (string, bool) {
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				v, e := strconv.Unquote(x.Value)
				return v, e == nil
			}
		case *ast.Ident:
			v, ok := values[x.Name]
			return v, ok
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				a, ok := eval(x.X)
				b, ok2 := eval(x.Y)
				return a + b, ok && ok2
			}
		}
		return "", false
	}
	// Resolve package string constants, including subscriberColumns.
	for _, d := range tree.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
			for _, s := range g.Specs {
				if v, ok := s.(*ast.ValueSpec); ok {
					for i, n := range v.Names {
						if i < len(v.Values) {
							if value, ok := eval(v.Values[i]); ok {
								values[n.Name] = value
							}
						}
					}
				}
			}
		}
	}
	matches := map[string]bool{}
	for _, d := range tree.Decls {
		f, ok := d.(*ast.FuncDecl)
		if !ok || f.Name.Name != function {
			continue
		}
		ast.Inspect(f.Body, func(n ast.Node) bool {
			if binary, ok := n.(*ast.BinaryExpr); ok && binary.Op == token.ADD {
				left := binary.X
				for {
					nested, ok := left.(*ast.BinaryExpr)
					if !ok {
						break
					}
					left = nested.X
				}
				if first, ok := eval(left); ok && strings.HasPrefix(first, prefix) {
					value, ok := eval(binary)
					if !ok {
						t.Fatalf("unresolved production SQL expression in %s %s", file, function)
					}
					matches[value] = true
					return false
				}
			}
			e, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			if value, ok := eval(e); ok && strings.HasPrefix(value, prefix) {
				matches[value] = true
			}
			return true
		})
	}
	// A prefix of a concatenated expression is also an AST node. Keep the full
	// expression and require that its candidate is unique.
	var longest string
	for q := range matches {
		if len(q) > len(longest) {
			longest = q
		}
	}
	if longest == "" {
		t.Fatalf("production SQL missing or cannot be resolved: %s %s %q", file, function, prefix)
	}
	for q := range matches {
		if !strings.HasPrefix(longest, q) {
			t.Fatalf("ambiguous production SQL: %s %s", file, function)
		}
	}
	return longest
}

func representativeDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = db.Writer.Exec(`
 WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<40)
 INSERT INTO brands(id,name,from_name,from_email,created_at) SELECT x,'Brand','Sender',printf('sender%d@example.test',x),1 FROM n;
 INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) SELECT id,id,printf('list%d',id),'List','single',1 FROM brands;
 INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) SELECT id,id,printf('campaign%d',id),'sending','Hi','<p>Hi</p>',1,1 FROM brands;
 INSERT INTO campaign_stats(campaign_id) SELECT id FROM campaigns;
 WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000)
 INSERT INTO subscribers(id,brand_id,email,fields,created_at,updated_at) SELECT x,(x-1)%40+1,printf('person%d@example.test',x),json_object('country',CASE WHEN x%101=1 THEN 'NL' ELSE 'US' END),x,x FROM n;
 INSERT INTO list_subscribers SELECT brand_id,id,'subscribed',1,1 FROM subscribers;
 INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,message_id) SELECT brand_id,id,CASE WHEN id%3=0 THEN 'sent' WHEN id%3=1 THEN 'pending' ELSE 'claimed' END,printf('msg%d',id) FROM subscribers;
 INSERT INTO opens(campaign_id,subscriber_id,at) SELECT brand_id,id,id FROM subscribers;
 INSERT INTO clicks(campaign_id,link_id,subscriber_id,at) SELECT brand_id,brand_id,id,id FROM subscribers;
 INSERT INTO ses_events(sns_message_id,ses_message_id,type,recipient,at,payload) SELECT printf('sns%d',id),printf('msg%d',id),'Delivery',email,id,'{}' FROM subscribers;
 INSERT INTO links(id,campaign_id,url) SELECT id,id,'https://example.test' FROM campaigns;
 INSERT INTO link_stats(link_id,campaign_id) SELECT id,campaign_id FROM links;
 INSERT INTO campaign_countries(campaign_id,country) SELECT id,'NL' FROM campaigns;
 INSERT INTO engagement_hourly(campaign_id,hour) SELECT id,1 FROM campaigns;
 INSERT INTO jobs(queue,payload,status,run_at,created_at) SELECT CASE WHEN id%40=1 THEN 'retention' ELSE 'other' END,'{}',CASE WHEN id%3=0 THEN 'done' ELSE 'pending' END,id,1 FROM subscribers;
 ANALYZE;`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func assertProductionPlan(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("EXPLAIN actual SQL: %v\n%s", err, q)
	}
	defer rows.Close()
	searched := false
	var details []string
	steps := 0
	// Match aliases as well as names. Index-backed SCAN is still a full scan.
	large := map[string]bool{"subscribers": true, "list_subscribers": true, "campaign_recipients": true, "opens": true, "clicks": true, "ses_events": true, "s": true, "ls": true, "r": true, "cr": true}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps++
		t.Log(detail)
		details = append(details, detail)
		fields := strings.Fields(detail)
		if len(fields) > 1 && fields[0] == "SCAN" && large[fields[1]] {
			t.Errorf("actual hot SQL performs an unbounded scan: %s", detail)
		}
		if strings.HasPrefix(detail, "SEARCH ") {
			searched = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if steps == 0 || (!searched && !strings.HasPrefix(q, "SELECT id FROM campaigns")) {
		t.Fatal("actual SQL has no SEARCH step")
	}
	if !searched {
		t.Log("Scheduler polling scans the small campaigns table; no indexed scheduler claim is made by this test.")
	}
	return details
}

func TestProductionHotQueryPlans(t *testing.T) {
	db := representativeDB(t)
	type proof struct {
		name, file, function, prefix string
		args                         []any
		variables                    map[string]string
	}
	tests := []proof{
		{"claimer", "../send/pipeline.go", "claim", "SELECT r.subscriber_id", []any{1, 40, 200}, nil},
		{"claim_update", "../send/pipeline.go", "claim", "UPDATE campaign_recipients", []any{1, 41}, nil},
		{"result_update", "../send/pipeline.go", "writeResults", "UPDATE campaign_recipients", []any{"sent", "newmsg", "", "sent", 100, 1, 41}, nil},
		{"message_id", "../events/events.go", "Process", "SELECT campaign_id,subscriber_id FROM campaign_recipients", []any{"msg41"}, nil},
		{"email", "../contacts/service.go", "GetByEmail", "SELECT ", []any{1, "person41@example.test"}, nil},
		{"contacts_page", "../contacts/service.go", "Search", "SELECT ", []any{1, 40, 100}, nil},
		{"memberships", "../httpapi/contacts.go", "subscribers", "SELECT ls.*", []any{1, 41}, nil},
		{"recipient_export", "../httpapi/campaigns.go", "exportRecipients", "SELECT subscriber_id,snapshot_email", []any{1, 40}, nil},
		{"campaign_stats", "../httpapi/campaigns.go", "report", "SELECT * FROM campaign_stats", []any{1}, nil},
		{"link_rollups", "../httpapi/campaigns.go", "report", "SELECT l.id,l.url", []any{1}, nil},
		{"country_rollups", "../httpapi/campaigns.go", "report", "SELECT country,opens", []any{1}, nil},
		{"hour_rollups", "../httpapi/campaigns.go", "report", "SELECT hour,opens", []any{1}, nil},
		{"brand_rollups", "../httpapi/brands.go", "overview", "SELECT COALESCE(SUM(s.sent)", []any{1}, nil},
		{"due_job_claim", "../jobs/runner.go", "claim", "UPDATE jobs", []any{1000, "retention", 500}, nil},
		{"due_campaign", "../send/engine.go", "Start", "SELECT id FROM campaigns", []any{500}, nil},
		{"open_erasure", "../contacts/manage.go", "Erase", "DELETE FROM opens", []any{41}, map[string]string{"table": "opens"}},
		{"click_erasure", "../contacts/manage.go", "Erase", "DELETE FROM clicks", []any{41}, map[string]string{"table": "clicks"}},
		{"ses_erasure_lookup", "../contacts/manage.go", "Erase", "DELETE FROM ses_events", []any{41, 1}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			q := sourceSQL(t, test.file, test.function, test.prefix, test.variables)
			assertProductionPlan(t, db.Reader, q, test.args...)
		})
	}
	// Count predicates are generated by the real compiler, and the enclosing SQL
	// expression is read from the real SegmentCount method.
	for _, rule := range []contacts.Rule{{Field: "list", Value: 1}, {Field: "opened", Value: 1}, {Field: "clicked", Value: 1}, {Field: "inactive", Value: 3}, {Field: "status", Op: "eq", Value: "active"}} {
		t.Run("segment_"+rule.Field, func(t *testing.T) {
			predicate, args, err := contacts.CompileSegment(rule)
			if err != nil {
				t.Fatal(err)
			}
			q := sourceSQL(t, "../contacts/segments.go", "SegmentCount", "SELECT count(*)", map[string]string{"q": predicate, "hint": ""})
			assertProductionPlan(t, db.Reader, q, append([]any{1}, args...)...)
		})
	}
}

func TestProductionCustomSegmentPlan(t *testing.T) {
	db := representativeDB(t)
	service := contacts.New(db)
	field, err := service.SaveCustomField(context.Background(), contacts.CustomField{BrandID: 1, Key: "country", Label: "Country", Type: "text", Filterable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	rule := contacts.Rule{Field: "fields.country", Op: "eq", Value: "NL"}
	predicate, args, err := contacts.CompileSegment(rule)
	if err != nil {
		t.Fatal(err)
	}
	format := sourceSQL(t, "../contacts/segments.go", "SegmentCount", " INDEXED BY subscribers_custom_", nil)
	q := sourceSQL(t, "../contacts/segments.go", "SegmentCount", "SELECT count(*)", map[string]string{"q": predicate, "hint": fmt.Sprintf(format, field.ID)})
	plan := assertProductionPlan(t, db.Reader, q, append([]any{1}, args...)...)
	if !strings.Contains(strings.Join(plan, "\n"), fmt.Sprintf("subscribers_custom_%d (brand_id=? AND <expr>=?)", field.ID)) {
		t.Fatal("custom count must SEARCH both brand and indexed expression")
	}
	count, err := service.SegmentCount(context.Background(), 1, rule)
	if err != nil || count == 0 {
		t.Fatalf("actual SegmentCount execution: count=%d error=%v", count, err)
	}
}

func TestProductionDynamicPagePlans(t *testing.T) {
	db := representativeDB(t)
	for _, status := range []string{"", "unknown", "sent"} {
		t.Run("recipients_"+status, func(t *testing.T) {
			q := sourceSQL(t, "../httpapi/campaigns.go", "report", "SELECT subscriber_id AS id", nil)
			args := []any{1, 40}
			if status != "" {
				q += sourceSQL(t, "../httpapi/campaigns.go", "report", " AND status=", nil)
				args = append(args, status)
			}
			q += sourceSQL(t, "../httpapi/campaigns.go", "report", " ORDER BY subscriber_id", nil)
			assertProductionPlan(t, db.Reader, q, append(args, 100)...)
		})
	}
	emailQuery := sourceSQL(t, "../contacts/service.go", "GetByEmail", "SELECT ", nil)
	columns := strings.TrimSuffix(strings.TrimPrefix(emailQuery, "SELECT "), " FROM subscribers WHERE brand_id=? AND email=?")
	for _, list := range []bool{false, true} {
		t.Run(fmt.Sprintf("list_export_%t", list), func(t *testing.T) {
			cols := columns
			args := []any{1, 40}
			if list {
				replacement := sourceSQL(t, "../contacts/csv.go", "ExportCSV", "fields,CASE", nil)
				cols = strings.Replace(cols, "fields,status,", replacement, 1)
				args = append([]any{1}, args...)
			}
			q := sourceSQL(t, "../contacts/csv.go", "ExportCSV", "SELECT ", map[string]string{"columns": cols})
			if list {
				q += sourceSQL(t, "../contacts/csv.go", "ExportCSV", " AND EXISTS", nil)
				args = append(args, 1)
			}
			q += sourceSQL(t, "../contacts/csv.go", "ExportCSV", " ORDER BY id", nil)
			assertProductionPlan(t, db.Reader, q, args...)
		})
	}
}
