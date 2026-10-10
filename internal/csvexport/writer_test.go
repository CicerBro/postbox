package csvexport

import (
	"bytes"
	"encoding/csv"
	"reflect"
	"testing"
)

func TestSafeCell(t *testing.T) {
	for _, value := range []string{"=1+1", "+SUM(A1:A2)", "-1+2", "@SUM(1)", " \t\r\n=HYPERLINK(\"https://example.org\")", "\x00\x1f+1", "\u00a0\u2003-1", "\ufeff\u200b@cmd", "＝1", "＋1", "－1", "＠cmd"} {
		if got := SafeCell(value); got != "'"+value {
			t.Errorf("SafeCell(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"", "  ", "Alice", "O'Brien", "'=1", "\tordinary", "雪, café \"quoted\"\nsecond line", "text=1", "123"} {
		if got := SafeCell(value); got != value {
			t.Errorf("SafeCell(%q) = %q", value, got)
		}
	}
}

func TestWriterPreservesCSVAndDoesNotMutateRecords(t *testing.T) {
	record := []string{"email", "雪, café \"quoted\"\nsecond line", "\t=1+1", "'=original", ""}
	original := append([]string(nil), record...)
	var out bytes.Buffer
	w := NewWriter(&out)
	if err := w.Write(record); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), original...)
	want[2] = "'" + want[2]
	if !reflect.DeepEqual(rows, [][]string{want}) || !reflect.DeepEqual(record, original) {
		t.Fatalf("rows = %#v, original record = %#v", rows, record)
	}
}
