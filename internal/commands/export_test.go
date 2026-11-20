package commands

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func exportFixture(h *harness) {
	seed(h, 5)
	h.f.pageSize = 2
	h.f.sessions[0]["title"] = `=HYPERLINK("http://evil","x")`
	h.f.sessions[1]["title"] = "Has, comma and \"quotes\"\nand a newline"
	h.f.sessions[2]["title"] = "+1 stays text"
}

func TestExportCSVToStdout(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	exportFixture(h)
	code, out, errOut := h.run("export", "sessions", "--format", "csv")
	eq(t, code, 0, "exit code: "+errOut)
	eq(t, h.f.count("GET", "/v1/sessions"), 3, "every page fetched (5 sessions, 2 per page)")
	if strings.HasPrefix(out, "\xef\xbb\xbf") {
		t.Fatal("no BOM unless asked")
	}
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("not valid RFC 4180 CSV: %v\n%s", err, out)
	}
	eq(t, len(rows), 6, "header plus five rows")
	eq(t, strings.Join(rows[0], ","), "id,title,status,stage,repos,owner,pullRequestNumber,pullRequestUrl,visibility,createdAt,lastActiveAt,url", "header")
	eq(t, rows[1][0], "ses_001", "id")
	eq(t, rows[1][1], `'=HYPERLINK("http://evil","x")`, "formula injection guarded")
	eq(t, rows[2][1], "Has, comma and \"quotes\"\nand a newline", "quoting round-trips")
	eq(t, rows[3][1], "'+1 stays text", "leading plus guarded")
	eq(t, rows[2][6], "102", "PR number")
	eq(t, rows[2][7], "https://github.com/acme/api/pull/102", "PR url")
	eq(t, rows[1][5], "maya", "owner")
	eq(t, rows[1][9], "2026-11-10T10:00:00Z", "ISO 8601 UTC")
	eq(t, rows[1][10], "2026-11-20T11:59:00Z", "lastActiveAt")
	eq(t, rows[1][11], h.f.srv.URL+"/s/ses_001", "url")
	notContains(t, errOut, "ses_")
}

func TestExportCSVToFileWithBOM(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 2)
	path := filepath.Join(t.TempDir(), "s.csv")
	code, out, errOut := h.run("export", "sessions", "--output", path, "--bom")
	eq(t, code, 0, "exit code")
	eq(t, out, "", "nothing on stdout when writing a file")
	contains(t, errOut, "Wrote 2 sessions")
	b, _ := os.ReadFile(path)
	if !bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		t.Fatal("expected a BOM")
	}
	code, out, _ = h.run("--json", "export", "sessions", "-o", path)
	eq(t, code, 0, "json exit code")
	m := mustJSON(t, out)
	eq(t, m["rows"], any(float64(2)), "rows")
	eq(t, m["file"], any(path), "file")
}

func TestExportXLSX(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	exportFixture(h)
	path := filepath.Join(t.TempDir(), "s.xlsx")
	code, _, errOut := h.run("export", "sessions", "--format", "xlsx", "--output", path, "--status", "idle")
	eq(t, code, 0, "exit code: "+errOut)
	eq(t, h.f.last("GET", "/v1/sessions").Query["status"][0], "idle", "filters apply to exports")
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	eq(t, f.GetSheetList()[0], "Sessions", "sheet name")
	rows, _ := f.GetRows("Sessions")
	eq(t, len(rows), 6, "header plus five rows")
	eq(t, rows[0][1], "title", "header cell")
	eq(t, rows[2][1], "Has, comma and \"quotes\"\nand a newline", "text cell")
	eq(t, rows[1][1], `'=HYPERLINK("http://evil","x")`, "formula injection guarded in xlsx too")
	// typed cells: numbers as numbers, dates as dates
	pr, _ := f.GetCellType("Sessions", "G3")
	if pr != excelize.CellTypeUnset && pr != excelize.CellTypeNumber {
		t.Fatalf("PR number should be a number cell, got %v", pr)
	}
	eq(t, rows[2][6], "102", "PR number value")
	style, _ := f.GetCellStyle("Sessions", "J2")
	st, _ := f.GetStyle(style)
	if st == nil || st.CustomNumFmt == nil || !strings.Contains(*st.CustomNumFmt, "yyyy") {
		t.Fatalf("createdAt should have a date format: %+v", st)
	}
	hs, _ := f.GetCellStyle("Sessions", "A1")
	hst, _ := f.GetStyle(hs)
	if hst == nil || hst.Font == nil || !hst.Font.Bold {
		t.Fatal("header should be bold")
	}
	panes, _ := f.GetPanes("Sessions")
	if !panes.Freeze || panes.YSplit != 1 {
		t.Fatalf("header should be frozen: %+v", panes)
	}
	formula, _ := f.GetCellFormula("Sessions", "L2")
	contains(t, formula, `HYPERLINK("`+h.f.srv.URL+`/s/ses_001"`)
	formula, _ = f.GetCellFormula("Sessions", "H3")
	contains(t, formula, "https://github.com/acme/api/pull/102")
	ws, _ := os.ReadFile(path)
	if !bytes.HasPrefix(ws, []byte("PK")) {
		t.Fatal("xlsx is a zip file")
	}
}

func TestExportXLSXToStdoutWhenPiped(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 1)
	code, out, _ := h.run("export", "sessions", "--format", "xlsx")
	eq(t, code, 0, "exit code")
	if !strings.HasPrefix(out, "PK") {
		t.Fatal("binary xlsx expected on a redirected stdout")
	}
}

func TestExportXLSXRefusedOnTerminal(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.tty = true
	code, out, errOut := h.run("export", "sessions", "--format", "xlsx")
	eq(t, code, 2, "exit code")
	eq(t, out, "", "nothing written")
	contains(t, errOut, "--output")
	eq(t, h.f.count("GET", "/v1/sessions"), 0, "refused before fetching")
}

func TestExportValidation(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, _ := h.run("export", "sessions", "--format", "pdf")
	eq(t, code, 2, "bad format")
	code, _, _ = h.run("--json", "export", "sessions")
	eq(t, code, 2, "--json without --output")
}
