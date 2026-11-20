package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elmkarami/waniis-cli/internal/style"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Fatalf("%s differs.\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

const barJSON = `{"kind":"bar","title":"Refunds by plan, May 2026","x":["Team","Business","Enterprise"],"series":[{"name":"Refunds","values":[7400,5100,12600]}],"format":"currency","currency":"USD","source":"Stripe · Redshift (finance.refunds)"}`
const multiBarJSON = `{"kind":"bar","title":"Refunds by plan, May 2026","x":["Week 1","Week 2","Week 3"],"series":[{"name":"Team","values":[7400,5100,3600]},{"name":"Business","values":[5000,3800,2700]}],"format":"currency","currency":"USD","source":"Stripe · Redshift (finance.refunds)"}`
const stackedJSON = `{"kind":"stacked_bar","title":"Tickets by status","x":["Mon","Tue","Wed"],"series":[{"name":"Open","values":[10,20,5]},{"name":"Closed","values":[30,10,25]}],"unit":"tickets"}`
const lineJSON = `{"kind":"line","title":"p95 latency","x":["Mon","Tue","Wed","Thu","Fri"],"series":[{"name":"API","values":[120,135,150,128,300]}],"unit":"ms","source":"Datadog"}`
const areaJSON = `{"kind":"area","title":"Signups","x":["Jan","Feb","Mar"],"series":[{"name":"Users","values":[12.5,20,17]}],"format":"percent"}`

func fence(body string) string { return "Here you go.\n```chart\n" + body + "\n```\nDone.\n" }

func md(text string, on bool, width int, draw bool) string {
	return FormatMarkdown(text, MDOptions{Style: style.Style{On: on}, Width: width, Draw: draw})
}

func TestChartGoldens(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		color bool
		width int
		draw  bool
	}{
		{"bar_wide", fence(barJSON), false, 80, true},
		{"bar_color", fence(barJSON), true, 80, true},
		{"bar_narrow", fence(barJSON), false, 40, true},
		{"bar_table_only", fence(barJSON), false, 80, false},
		{"multibar_wide", fence(multiBarJSON), false, 80, true},
		{"multibar_color", fence(multiBarJSON), true, 80, true},
		{"stacked_wide", fence(stackedJSON), false, 70, true},
		{"line_wide", fence(lineJSON), false, 80, true},
		{"line_color", fence(lineJSON), true, 80, true},
		{"area_wide", fence(areaJSON), false, 80, true},
		{"malformed", fence(`{"kind":"pie","title":"x"}`), false, 80, true},
		{"malformed_json", fence(`{not json`), false, 80, true},
		{"markdown_table", "Totals:\n\n| Plan | Count | Share |\n|---|--:|:-:|\n| Team | 1,200 | 40% |\n| Business | 300 | 10% |\n\nThat's all.\n", false, 80, true},
		{"markdown_table_color", "| Name | Amount |\n|---|---|\n| a | 12 |\n| bb | 1,234 |\n", true, 80, true},
		{"table_in_code_untouched", "```\n| a | b |\n|---|---|\n| 1 | 2 |\n```\n", false, 80, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { golden(t, c.name, md(c.text, c.color, c.width, c.draw)) })
	}
}

func TestChunkingDoesNotChangeOutput(t *testing.T) {
	text := fence(multiBarJSON) + "| a | b |\n|---|---|\n| 1 | 2 |\n"
	want := md(text, false, 80, true)
	var b strings.Builder
	m := NewMD(&b, MDOptions{Width: 80, Draw: true})
	for _, r := range text { // one character at a time
		m.Write(string(r))
	}
	m.Flush()
	if b.String() != want {
		t.Fatalf("chunked output differs:\n%s\nwant\n%s", b.String(), want)
	}
}

func TestStreamedLinesAppearBeforeTheyEnd(t *testing.T) {
	var b strings.Builder
	m := NewMD(&b, MDOptions{})
	m.Write("Hello wor")
	if b.String() != "Hello wor" {
		t.Fatalf("got %q", b.String())
	}
	m.Write("ld\n")
	m.Flush()
	if b.String() != "Hello world\n" {
		t.Fatalf("got %q", b.String())
	}
}

func TestUnclosedChartFenceIsPrintedAsWritten(t *testing.T) {
	out := md("```chart\n{\"kind\":", false, 80, true)
	if out != "```chart\n{\"kind\":\n" {
		t.Fatalf("got %q", out)
	}
}
