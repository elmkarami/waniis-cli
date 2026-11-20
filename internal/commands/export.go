package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/export"
	"github.com/elmkarami/waniis-cli/internal/output"
)

func (a *app) exportCmd() *cobra.Command {
	c := &cobra.Command{Use: "export", Short: "Export data to CSV or Excel"}
	c.AddCommand(a.exportSessionsCmd())
	return c
}

var sessionExportHeaders = []string{"id", "title", "status", "stage", "repos", "owner", "pullRequestNumber",
	"pullRequestUrl", "visibility", "createdAt", "lastActiveAt", "url"}

// sessionExportTable lays sessions out as an export: ISO timestamps, one row per session.
func sessionExportTable(base string, items []api.Session) export.Table {
	t := export.Table{Sheet: "Sessions", Headers: sessionExportHeaders}
	for _, s := range items {
		u := sessionURL(base, s.ID)
		owner := ""
		if s.Owner != nil {
			owner = firstNonEmpty(s.Owner.Login, s.Owner.Name)
		}
		var prNum any
		var prURL any
		if s.PullRequest != nil {
			prNum = s.PullRequest.Number
			prURL = export.Link{URL: s.PullRequest.URL}
		}
		repos := make([]string, 0, len(s.Repos))
		for _, r := range s.Repos {
			repos = append(repos, r.FullName)
		}
		t.Rows = append(t.Rows, []any{s.ID, s.Title, s.Status, s.Stage, strings.Join(repos, "; "),
			owner, prNum, prURL, s.Visibility, s.CreatedAt, s.LastActiveAt, export.Link{URL: u}})
	}
	return t
}

func (a *app) exportSessionsCmd() *cobra.Command {
	var f listFlags
	var format, outFile string
	var bom bool
	c := &cobra.Command{
		Use:   "sessions",
		Short: "Export sessions to CSV or Excel",
		Long: `Export every session that matches the filters (all pages are fetched) as CSV or XLSX.
CSV is RFC 4180 in UTF-8 with ISO 8601 UTC timestamps; cells that start with = + - or @ are
prefixed with ' so a spreadsheet can't run them. XLSX has a bold frozen header, an autofilter,
real dates and links. XLSX is never written to a terminal: use --output or redirect.`,
		Example: `  waniis export sessions --format csv --output sessions.csv
  waniis export sessions --format xlsx --repo acme/api --since 2026-11-01 --output sessions.xlsx
  waniis export sessions --status failed > failed.csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch format {
			case export.CSV, export.XLSX:
			default:
				return exitcode.Usagef("--format must be csv or xlsx")
			}
			if format == export.XLSX && outFile == "" && a.StdoutTTY {
				return exitcode.Usagef("won't write a binary XLSX file to the terminal").
					WithHint("Use --output file.xlsx or redirect: waniis export sessions --format xlsx > file.xlsx")
			}
			if a.useJSON() && outFile == "" {
				return exitcode.Usagef("--json needs --output (it reports what was written); without it the export goes to stdout")
			}
			filter, err := a.toFilter(f)
			if err != nil {
				return err
			}
			filter.Limit = maxPageSize
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			items, _, _, err := a.collect(cmd.Context(), rt.Client.ListSessions, filter, true)
			if err != nil {
				return err
			}
			if a.Err != nil && a.interactive() {
				fmt.Fprint(a.Err, "\r")
			}
			table := sessionExportTable(rt.Base, items)
			var buf bytes.Buffer
			if format == export.CSV {
				err = export.WriteCSV(&buf, table, bom)
			} else {
				err = export.WriteXLSX(&buf, table)
			}
			if err != nil {
				return exitcode.New(exitcode.General, "export_failed", "couldn't build the export: "+err.Error())
			}
			if outFile == "" {
				_, err = io.Copy(a.Out, &buf)
				return err
			}
			if err := os.WriteFile(outFile, buf.Bytes(), 0o644); err != nil {
				return exitcode.New(exitcode.General, "write_failed", "couldn't write "+outFile+": "+err.Error())
			}
			if a.useJSON() {
				return output.JSON(a.Out, map[string]any{"file": outFile, "format": format, "rows": len(items)})
			}
			fmt.Fprintf(a.Err, "Wrote %d sessions to %s\n", len(items), outFile)
			return nil
		},
	}
	addListFlags(c, &f, false)
	c.Flags().StringVar(&format, "format", "csv", "csv|xlsx")
	c.Flags().StringVarP(&outFile, "output", "o", "", "write to this file instead of stdout")
	c.Flags().BoolVar(&bom, "bom", false, "start CSV with a UTF-8 byte order mark (Excel on Windows)")
	return c
}
