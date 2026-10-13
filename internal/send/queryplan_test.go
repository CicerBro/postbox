package send

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"postbox/internal/subscribers"
)

// Read the complete SQL concatenation from the running implementation, injecting
// only the fragment returned by the real audience builder.
func audienceStatement(t *testing.T, function, prefix, audience string) string {
	t.Helper()
	tree, err := parser.ParseFile(token.NewFileSet(), "engine.go", nil, 0)
	if function == "beginAttempt" {
		tree, err = parser.ParseFile(token.NewFileSet(), "pipeline.go", nil, 0)
	}
	if function == "finishDispatchTx" {
		tree, err = parser.ParseFile(token.NewFileSet(), "dispatch_ledger.go", nil, 0)
	}
	if err != nil {
		t.Fatal(err)
	}
	var eval func(ast.Expr) (string, bool)
	eval = func(e ast.Expr) (string, bool) {
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				s, e := strconv.Unquote(x.Value)
				return s, e == nil
			}
		case *ast.Ident:
			if x.Name == "q" {
				return audience, true
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "audience" {
				return audience, true
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				a, ok := eval(x.X)
				b, ok2 := eval(x.Y)
				return a + b, ok && ok2
			}
		}
		return "", false
	}
	var queries []string
	for _, d := range tree.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "QueryRowContext" && sel.Sel.Name != "ExecContext") || len(call.Args) < 2 {
				return true
			}
			if q, ok := eval(call.Args[1]); ok && strings.HasPrefix(q, prefix) {
				queries = append(queries, q)
			}
			return true
		})
	}
	if len(queries) != 1 {
		t.Fatalf("expected one complete production %s statement, got %d", function, len(queries))
	}
	return queries[0]
}

func TestProductionDispatchCompletionPlan(t *testing.T) {
	db, _, _ := fixture(t, 1)
	if _, err := db.Writer.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<30000)
 INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at)
 SELECT 1,CAST(printf('%032d',x) AS BLOB),'started',1,x,1,1 FROM n`); err != nil {
		t.Fatal(err)
	}
	q := audienceStatement(t, "finishDispatchTx", "UPDATE campaign_dispatch_ledger", "")
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			if _, err := db.Writer.Exec("ANALYZE"); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := db.Reader.Query("EXPLAIN QUERY PLAN "+q, "sent", 1, 1, 15000, 1, 1, 15000, 1)
		if err != nil {
			t.Fatal(err)
		}
		ownerLookup, journalLookup := false, false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Logf("analyzed=%v %s", analyzed, detail)
			if strings.Contains(detail, "campaign_dispatch_ledger") {
				ownerLookup = strings.HasPrefix(detail, "SEARCH ") && strings.Contains(detail, "campaign_id=? AND owner_subscriber_id=? AND owner_attempt=?")
			}
			if strings.Contains(detail, "send_attempts") {
				journalLookup = strings.HasPrefix(detail, "SEARCH ") && strings.Contains(detail, "campaign_id=? AND subscriber_id=? AND attempt=?")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !ownerLookup || !journalLookup {
			t.Fatalf("completion must constrain both owner and journal to one attempt: owner=%v journal=%v", ownerLookup, journalLookup)
		}
	}
}

func TestProductionAudiencePlans(t *testing.T) {
	db, e, _ := fixture(t, 400)
	_, err := db.Writer.Exec(`
 INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(2,'Other','Sender','other@example.test',1);
 WITH RECURSIVE n(x) AS (VALUES(401) UNION ALL SELECT x+1 FROM n WHERE x<10000)
 INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) SELECT x,2,printf('other%d@example.test',x),1,1 FROM n;
 INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(3,1,'excluded','Excluded','single',1);
 INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) SELECT 3,id,'subscribed',1,1 FROM subscribers WHERE brand_id=1 AND id%7=0;
 UPDATE subscribers SET fields=json_object('country',CASE WHEN id%5=0 THEN 'UK' ELSE 'US' END);
 INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(1,'email','reader1@example.org','manual',1),(NULL,'domain','blocked.example','manual',1);`)
	if err != nil {
		t.Fatal(err)
	}
	service := subscribers.New(db)
	_, err = service.SaveCustomField(context.Background(), subscribers.CustomField{BrandID: 1, Key: "country", Label: "Country", Type: "text", Filterable: true})
	if err != nil {
		t.Fatal(err)
	}
	segment, err := service.SaveSegment(context.Background(), subscribers.Segment{BrandID: 1, Name: "UK", Rules: subscribers.Rule{Field: "fields.country", Op: "eq", Value: "UK"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                                        string
		includeSegment, excludeList, excludeSegment bool
	}{
		{"include_list", false, false, false},
		{"include_list_exclude_list", false, true, false},
		{"include_segment", true, false, false},
		{"include_list_exclude_segment", false, false, true},
		{"include_segment_exclude_list", true, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Writer.Exec("DELETE FROM campaign_targets WHERE campaign_id=1"); err != nil {
				t.Fatal(err)
			}
			kind, id := "list", int64(1)
			if test.includeSegment {
				kind, id = "segment", segment.ID
			}
			if _, err := db.Writer.Exec("INSERT INTO campaign_targets VALUES(1,?,?,'include')", kind, id); err != nil {
				t.Fatal(err)
			}
			if test.excludeList {
				if _, err := db.Writer.Exec("INSERT INTO campaign_targets VALUES(1,'list',3,'exclude')"); err != nil {
					t.Fatal(err)
				}
			}
			if test.excludeSegment {
				if _, err := db.Writer.Exec("INSERT INTO campaign_targets VALUES(1,'segment',?,'exclude')", segment.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Writer.Exec("ANALYZE"); err != nil {
				t.Fatal(err)
			}
			audience, args, err := e.audienceSQL(context.Background(), campaign{id: 1, brand: 1})
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range []struct {
				fn, prefix string
				args       []any
			}{
				{"snapshot", "INSERT INTO campaign_recipients", append([]any{1}, args...)},
				{"beginAttempt", "SELECT EXISTS(SELECT 1 FROM subscribers s", append(append([]any{}, args...), 41, "identity")},
			} {
				q := audienceStatement(t, statement.fn, statement.prefix, audience)
				rows, err := db.Reader.Query("EXPLAIN QUERY PLAN "+q, statement.args...)
				if err != nil {
					t.Fatal(err)
				}
				searches := 0
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					t.Log(statement.fn, detail)
					f := strings.Fields(detail)
					if len(f) > 1 && f[0] == "SCAN" && (f[1] == "s" || f[1] == "ls" || f[1] == "cr" || f[1] == "subscribers" || f[1] == "list_subscribers" || f[1] == "campaign_recipients") {
						t.Errorf("actual audience performs unbounded scan: %s", detail)
					}
					if strings.HasPrefix(detail, "SEARCH ") {
						searches++
					}
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				if searches == 0 {
					t.Error("actual audience has no SEARCH step")
				}
			}
			// Execute the exact snapshot statement as well, to validate argument order,
			// target composition and the ON CONFLICT suffix, not just SQL compilation.
			q := audienceStatement(t, "snapshot", "INSERT INTO campaign_recipients", audience)
			if _, err := db.Writer.Exec("DELETE FROM campaign_recipients WHERE campaign_id=1"); err != nil {
				t.Fatal(err)
			}
			res, err := db.Writer.Exec(q, append([]any{1}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			n, err := res.RowsAffected()
			if err != nil || n == 0 {
				t.Fatalf("snapshot inserted %d: %v", n, err)
			}
		})
	}
}
