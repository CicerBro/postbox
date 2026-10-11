package migrate

import (
	"context"
	"strings"
	"testing"
)

func TestDumpCommentTerminatorsPreserveRows(t *testing.T) {
	for _, prefix := range []string{";", "/*!40101 SET NAMES utf8mb4 */;", "/*M!100100 SET NAMES utf8mb4 */;"} {
		t.Run(prefix, func(t *testing.T) {
			dump := prefix + `
CREATE TABLE apps (id int, app_name text);
LOCK TABLES apps WRITE;
/*!40000 ALTER TABLE apps DISABLE KEYS */;
INSERT INTO apps VALUES (1,'Brand');
/*!40000 ALTER TABLE apps ENABLE KEYS */;
UNLOCK TABLES;
CREATE TABLE lists (id int, app int, name text);
LOCK TABLES lists WRITE;
/*!40000 ALTER TABLE lists DISABLE KEYS */;
INSERT INTO lists VALUES (10,1,'Audience');
/*!40000 ALTER TABLE lists ENABLE KEYS */;
UNLOCK TABLES;
`
			var rows []Row
			err := (DumpSource{Reader: strings.NewReader(dump)}).Scan(context.Background(), func(row Row) error {
				rows = append(rows, row)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 {
				t.Fatalf("got %d rows, want both apps and lists rows", len(rows))
			}
			for i, want := range []struct{ table, id, column, value string }{
				{"apps", "1", "app_name", "Brand"},
				{"lists", "10", "name", "Audience"},
			} {
				row := rows[i]
				if row.Table != want.table || row.get("id") != want.id || row.get(want.column) != want.value {
					t.Fatalf("unexpected row %d: %+v", i, row)
				}
			}
		})
	}
}
