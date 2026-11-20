// Package export writes tabular data as CSV (RFC 4180, guarded against formula injection) or XLSX.
package export

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Link is a hyperlink cell.
type Link struct {
	URL  string
	Text string
}

// Table is what gets exported. Cells are string, int, int64, float64, bool, time.Time, Link or nil.
type Table struct {
	Sheet   string
	Headers []string
	Rows    [][]any
}

// Format names the supported export formats.
const (
	CSV  = "csv"
	XLSX = "xlsx"
)

// bom is the UTF-8 byte order mark that Excel on Windows needs to read UTF-8 CSV.
const bom = "\xef\xbb\xbf"

// GuardCell prefixes cells that a spreadsheet would read as a formula with an apostrophe, so
// exported data can't run anything when opened (CSV/formula injection).
func GuardCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// CellText renders a cell as CSV text: ISO 8601 UTC timestamps, plain numbers.
func CellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return GuardCell(x)
	case Link:
		return GuardCell(x.URL)
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return x.UTC().Format(time.RFC3339)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return GuardCell(fmt.Sprint(v))
}

// WriteCSV writes the table as RFC 4180 CSV in UTF-8, with a header row. withBOM prepends a BOM.
func WriteCSV(w io.Writer, t Table, withBOM bool) error {
	if withBOM {
		if _, err := io.WriteString(w, bom); err != nil {
			return err
		}
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(t.Headers); err != nil {
		return err
	}
	for _, row := range t.Rows {
		rec := make([]string, len(row))
		for i, c := range row {
			rec[i] = CellText(c)
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
