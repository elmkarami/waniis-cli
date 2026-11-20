//go:build !(386 || arm || mips || mipsle)

package export

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// XLSXSupported reports whether this build can write Excel files (64-bit builds only: the Excel
// library doesn't compile for 32-bit CPUs).
const XLSXSupported = true

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
