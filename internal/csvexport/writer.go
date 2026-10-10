// Package csvexport writes spreadsheet-safe CSV downloads.
package csvexport

import (
	"encoding/csv"
	"io"
	"unicode"
)

// Writer applies the same formula-injection policy to every cell, including
// headers, before delegating quoting and escaping to encoding/csv.
type Writer struct{ writer *csv.Writer }

func NewWriter(w io.Writer) *Writer { return &Writer{writer: csv.NewWriter(w)} }

// SafeCell preserves the original text, adding an apostrophe when its first
// meaningful rune is a formula prefix. Ignore leading whitespace, controls and
// Unicode format characters (including BOMs) when looking for that prefix.
// This is a spreadsheet download format, not a lossless machine backup format:
// importers must not silently strip the apostrophe, which may be original data.
func SafeCell(value string) string {
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		switch r {
		case '=', '+', '-', '@', '＝', '＋', '－', '＠':
			return "'" + value
		}
		break
	}
	return value
}

func (w *Writer) Write(record []string) error {
	safe := make([]string, len(record))
	for i, cell := range record {
		safe[i] = SafeCell(cell)
	}
	return w.writer.Write(safe)
}

func (w *Writer) Flush()       { w.writer.Flush() }
func (w *Writer) Error() error { return w.writer.Error() }
