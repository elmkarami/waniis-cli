// Package chart reads the ```chart blocks that Waniis writes into its answers (DEC-156) and draws
// them for a terminal: always a table, and, on a wide enough terminal, a bar or line plot.
//
// The schema and the validation rules mirror the server's answer-chart module: a block that
// doesn't satisfy them is not a chart and stays the code block the model wrote.
package chart

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Limits are the server's limits for a chart block.
const (
	MaxJSONChars = 6000
	MaxPoints    = 24
	MaxSeries    = 4
	MaxTitle     = 120
	MaxLabel     = 32
	MaxSeriesNme = 40
	MaxUnit      = 16
	MaxSource    = 200
	MaxMagnitude = 1e15
)

// Fence language of a chart block.
const FenceLanguage = "chart"

// Series is one named run of values.
type Series struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}

// Chart is a parsed chart block. The JSON tags are the block's own, so a Chart marshals back to
// the shape the model wrote (--json emits it as is).
type Chart struct {
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	X        []string `json:"x"`
	Series   []Series `json:"series"`
	Format   string   `json:"format"`
	Currency string   `json:"currency,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Source   string   `json:"source,omitempty"`
}

// ErrNotChart is wrapped by every Parse failure.
var ErrNotChart = errors.New("not a chart")

type rawSeries struct {
	Name   string `json:"name"`
	Values []any  `json:"values"`
}

type rawChart struct {
	Kind     string      `json:"kind"`
	Title    string      `json:"title"`
	X        []any       `json:"x"`
	Series   []rawSeries `json:"series"`
	Format   string      `json:"format"`
	Currency string      `json:"currency"`
	Unit     string      `json:"unit"`
	Source   string      `json:"source"`
}

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotChart, fmt.Sprintf(format, args...))
}

func text(s string, max int, what string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", bad("%s is empty", what)
	}
	if utf8.RuneCountInString(s) > max {
		return "", bad("%s is too long", what)
	}
	return s, nil
}

// Parse reads a chart block's body. It never panics; the error says why it isn't a chart.
func Parse(src string) (*Chart, error) {
	if utf8.RuneCountInString(src) > MaxJSONChars {
		return nil, bad("too large")
	}
	var r rawChart
	if err := json.Unmarshal([]byte(src), &r); err != nil {
		return nil, bad("invalid JSON")
	}
	c := &Chart{Format: r.Format}
	switch r.Kind {
	case "bar", "stacked_bar", "line", "area":
		c.Kind = r.Kind
	default:
		return nil, bad("unknown kind")
	}
	var err error
	if c.Title, err = text(r.Title, MaxTitle, "title"); err != nil {
		return nil, err
	}
	if len(r.X) < 1 || len(r.X) > MaxPoints {
		return nil, bad("x needs 1 to %d labels", MaxPoints)
	}
	for _, v := range r.X {
		s, ok := v.(string)
		if !ok {
			return nil, bad("x labels must be strings")
		}
		if s, err = text(s, MaxLabel, "an x label"); err != nil {
			return nil, err
		}
		c.X = append(c.X, s)
	}
	if len(r.Series) < 1 || len(r.Series) > MaxSeries {
		return nil, bad("series needs 1 to %d entries", MaxSeries)
	}
	seen := map[string]bool{}
	for _, rs := range r.Series {
		s := Series{}
		if s.Name, err = text(rs.Name, MaxSeriesNme, "a series name"); err != nil {
			return nil, err
		}
		if seen[strings.ToLower(s.Name)] {
			return nil, bad("series names repeat")
		}
		seen[strings.ToLower(s.Name)] = true
		if len(rs.Values) < 1 || len(rs.Values) > MaxPoints {
			return nil, bad("a series needs 1 to %d values", MaxPoints)
		}
		for _, v := range rs.Values {
			f, ok := v.(float64)
			if !ok || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > MaxMagnitude {
				return nil, bad("values must be plain numbers")
			}
			if c.Kind == "stacked_bar" && f < 0 {
				return nil, bad("a stacked bar can't stack negative values")
			}
			s.Values = append(s.Values, f)
		}
		if len(s.Values) != len(c.X) {
			return nil, bad("%d values for %d x labels", len(s.Values), len(c.X))
		}
		c.Series = append(c.Series, s)
	}
	switch c.Format {
	case "":
		c.Format = "number"
	case "number", "currency", "percent":
	default:
		return nil, bad("unknown format")
	}
	if r.Currency != "" {
		if len(r.Currency) != 3 || strings.ToUpper(r.Currency) != r.Currency || strings.IndexFunc(r.Currency, func(x rune) bool { return x < 'A' || x > 'Z' }) >= 0 {
			return nil, bad("currency must be an ISO code")
		}
		c.Currency = r.Currency
	}
	if c.Format == "currency" && c.Currency == "" {
		return nil, bad("currency needs its code")
	}
	if r.Unit != "" {
		if c.Unit, err = text(r.Unit, MaxUnit, "unit"); err != nil {
			return nil, err
		}
	}
	if r.Source != "" {
		if c.Source, err = text(r.Source, MaxSource, "source"); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Fence is a fenced code block in Markdown.
type Fence struct {
	Language string // lower case first word of the info string, or ""
	Start    int    // byte offsets of the whole block, fences included
	End      int
	Code     string
	Closed   bool
}

// Fences finds every fenced code block in markdown (CommonMark's rules, enough for answers).
func Fences(markdown string) []Fence {
	var out []Fence
	offset := 0
	type open struct {
		marker, lang    string
		start, bodyFrom int
	}
	var cur *open
	for _, line := range strings.Split(markdown, "\n") {
		lineEnd := offset + len(line)
		bare := strings.TrimSuffix(line, "\r")
		if cur != nil {
			if m, ok := closingFence(bare); ok && m[0] == cur.marker[0] && len(m) >= len(cur.marker) {
				body := offset - 1
				if body < cur.bodyFrom {
					body = cur.bodyFrom
				}
				out = append(out, Fence{Language: cur.lang, Start: cur.start, End: lineEnd, Code: markdown[cur.bodyFrom:body], Closed: true})
				cur = nil
			}
		} else if m, lang, ok := openingFence(bare); ok {
			from := lineEnd + 1
			if from > len(markdown) {
				from = len(markdown)
			}
			cur = &open{marker: m, lang: lang, start: offset, bodyFrom: from}
		}
		offset = lineEnd + 1
	}
	if cur != nil {
		out = append(out, Fence{Language: cur.lang, Start: cur.start, End: len(markdown), Code: markdown[cur.bodyFrom:], Closed: false})
	}
	return out
}

func fenceRun(s string) (marker, rest string) {
	i := 0
	for i < 3 && i < len(s) && s[i] == ' ' {
		i++
	}
	s = s[i:]
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return "", ""
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return "", ""
	}
	return s[:n], s[n:]
}

func openingFence(line string) (marker, lang string, ok bool) {
	m, rest := fenceRun(line)
	if m == "" {
		return "", "", false
	}
	if m[0] == '`' && strings.Contains(rest, "`") {
		return "", "", false
	}
	if f := strings.Fields(rest); len(f) > 0 {
		lang = strings.ToLower(f[0])
	}
	return m, lang, true
}

func closingFence(line string) (string, bool) {
	m, rest := fenceRun(line)
	if m == "" || strings.TrimSpace(rest) != "" {
		return "", false
	}
	return m, true
}
