// Package export writes tabular data as CSV (RFC 4180, guarded against formula injection) or XLSX.
package export

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
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

// WriteXLSX writes the table as one sheet: bold frozen header, autofilter, typed cells (dates as
// dates, numbers as numbers) and hyperlinks. Rows go through the streaming writer so a very large
// export stays in bounded memory.
func WriteXLSX(w io.Writer, t Table) error {
	f := excelize.NewFile()
	defer f.Close()
	sheet := t.Sheet
	if sheet == "" {
		sheet = "Export"
	}
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}
	header, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return err
	}
	dateFmt := "yyyy-mm-dd hh:mm:ss"
	date, err := f.NewStyle(&excelize.Style{CustomNumFmt: &dateFmt})
	if err != nil {
		return err
	}
	link, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "0563C1", Underline: "single"}})
	if err != nil {
		return err
	}
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	if err := sw.SetPanes(&excelize.Panes{Freeze: true, Split: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	hdr := make([]any, len(t.Headers))
	for i, h := range t.Headers {
		hdr[i] = excelize.Cell{Value: h, StyleID: header}
	}
	if err := sw.SetRow("A1", hdr); err != nil {
		return err
	}
	for r, row := range t.Rows {
		cells := make([]any, len(row))
		for i, c := range row {
			cells[i] = xlsxCell(c, date, link)
		}
		cell, err := excelize.CoordinatesToCellName(1, r+2)
		if err != nil {
			return err
		}
		if err := sw.SetRow(cell, cells); err != nil {
			return err
		}
	}
	if err := sw.Flush(); err != nil {
		return err
	}
	if len(t.Headers) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(t.Headers), len(t.Rows)+1)
		if err := f.AutoFilter(sheet, "A1:"+last, nil); err != nil {
			return err
		}
	}
	return f.Write(w)
}

func xlsxCell(v any, dateStyle, linkStyle int) any {
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		if x.IsZero() {
			return nil
		}
		// Excel has no time zones: store UTC wall-clock time, as the header says.
		return excelize.Cell{Value: x.UTC(), StyleID: dateStyle}
	case Link:
		if x.URL == "" {
			return nil
		}
		if !strings.HasPrefix(x.URL, "https://") && !strings.HasPrefix(x.URL, "http://") {
			return GuardCell(x.URL) // never build a formula around anything but a web link
		}
		text := x.Text
		if text == "" {
			text = x.URL
		}
		return excelize.Cell{StyleID: linkStyle, Formula: fmt.Sprintf(`HYPERLINK("%s","%s")`, quote(x.URL), quote(text)), Value: text}
	case string:
		return GuardCell(x)
	case int, int64, float64, bool:
		return x
	}
	return GuardCell(fmt.Sprint(v))
}

// quote escapes a string for use inside a formula string literal.
func quote(s string) string { return strings.ReplaceAll(s, `"`, `""`) }
