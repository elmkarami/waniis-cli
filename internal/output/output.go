// Package output renders results: aligned tables for people, JSON for machines.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

// JSON writes v as indented JSON followed by a newline. HTML characters are not escaped.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table writes headers and rows as an aligned table. A table without rows writes nothing.
func Table(w io.Writer, headers []string, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(clean(r), "\t"))
	}
	return tw.Flush()
}

// KeyValues writes label/value pairs as an aligned block.
func KeyValues(w io.Writer, pairs [][2]string) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, p := range pairs {
		fmt.Fprintf(tw, "%s:\t%s\n", p[0], p[1])
	}
	return tw.Flush()
}

// Truncate shortens s to at most n runes, ending with an ellipsis.
func Truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// clean keeps cell text on one line so the table stays aligned.
func clean(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(c)
	}
	return out
}
