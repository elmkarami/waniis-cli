package export

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestGuardCell(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx", "plain": "plain", "": "", "a=b": "a=b"} {
		if got := GuardCell(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestWriteCSVRFC4180(t *testing.T) {
	var b bytes.Buffer
	tbl := Table{Headers: []string{"a", "b", "c"}, Rows: [][]any{
		{"x,y", `q"uote`, time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))},
		{1, 2.5, nil},
	}}
	if err := WriteCSV(&b, tbl, false); err != nil {
		t.Fatal(err)
	}
	want := "a,b,c\r\n\"x,y\",\"q\"\"uote\",2026-01-02T02:04:05Z\r\n1,2.5,\r\n"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
	b.Reset()
	_ = WriteCSV(&b, tbl, true)
	if !strings.HasPrefix(b.String(), "\xef\xbb\xbf") {
		t.Fatal("BOM")
	}
}

func TestXLSXTypedCells(t *testing.T) {
	var b bytes.Buffer
	when := time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)
	tbl := Table{Headers: []string{"name", "n", "when", "link", "evil"}, Rows: [][]any{
		{"a", 42, when, Link{URL: "https://x.example/s/1"}, "=cmd|' /C calc'!A0"},
		{"b", 1.5, nil, Link{URL: "javascript:alert(1)"}, nil},
	}}
	if err := WriteXLSX(&b, tbl); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(&b)
	if err != nil {
		t.Fatal(err)
	}
	if f.GetSheetList()[0] != "Export" {
		t.Fatal("default sheet name")
	}
	if v, _ := f.GetCellValue("Export", "B2"); v != "42" {
		t.Fatalf("number cell: %q", v)
	}
	if typ, _ := f.GetCellType("Export", "B2"); typ != excelize.CellTypeUnset && typ != excelize.CellTypeNumber {
		t.Fatalf("B2 type %v", typ)
	}
	if v, _ := f.GetCellValue("Export", "E2"); !strings.HasPrefix(v, "'=") {
		t.Fatalf("injection guard: %q", v)
	}
	if fm, _ := f.GetCellFormula("Export", "D3"); fm != "" {
		t.Fatalf("a non-web link must never become a formula: %q", fm)
	}
	if fm, _ := f.GetCellFormula("Export", "D2"); !strings.Contains(fm, "https://x.example/s/1") {
		t.Fatalf("link formula: %q", fm)
	}
	raw, _ := f.GetCellValue("Export", "C2", excelize.Options{RawCellValue: true})
	if raw == "" || raw == when.Format(time.RFC3339) {
		t.Fatalf("date should be stored as a serial number, got %q", raw)
	}
}

func TestXLSXLargeExportStreams(t *testing.T) {
	rows := make([][]any, 20000)
	for i := range rows {
		rows[i] = []any{i, "row"}
	}
	var b bytes.Buffer
	if err := WriteXLSX(&b, Table{Headers: []string{"i", "s"}, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenReader(&b)
	r, _ := f.Rows("Export")
	n := 0
	for r.Next() {
		n++
	}
	if n != 20001 {
		t.Fatalf("rows = %d", n)
	}
}
