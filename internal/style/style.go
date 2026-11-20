// Package style holds the terminal's colors and aligned tables. Colors are off unless asked for.
package style

import (
	"strings"
	"unicode/utf8"
)

// Style wraps text in ANSI colors when On.
type Style struct{ On bool }

func (s Style) wrap(code, text string) string {
	if !s.On || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string    { return s.wrap("1", t) }
func (s Style) Dim(t string) string     { return s.wrap("2", t) }
func (s Style) Red(t string) string     { return s.wrap("31", t) }
func (s Style) Green(t string) string   { return s.wrap("32", t) }
func (s Style) Yellow(t string) string  { return s.wrap("33", t) }
func (s Style) Magenta(t string) string { return s.wrap("35", t) }
func (s Style) Cyan(t string) string    { return s.wrap("36", t) }

// Reverse swaps the text and background colors: a selected tab.
func (s Style) Reverse(t string) string { return s.wrap("7", t) }

// Code wraps text in the given SGR codes ("1;36").
func (s Style) Code(code, t string) string { return s.wrap(code, t) }

// seriesCodes is the fixed order of series colors in charts.
var seriesCodes = []string{"36", "33", "35", "32"}

// Series colors text with the i-th series color.
func (s Style) Series(i int, t string) string { return s.wrap(seriesCodes[i%len(seriesCodes)], t) }

// Width is the printed width of s, ignoring ANSI sequences.
func Width(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if j := strings.IndexByte(s[i:], 'm'); j >= 0 {
				i += j + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// Align says how a column's cells are placed.
type Align int

const (
	Left Align = iota
	Right
	Center
)

func pad(cell string, w int, a Align) string {
	gap := w - Width(cell)
	if gap <= 0 {
		return cell
	}
	switch a {
	case Right:
		return strings.Repeat(" ", gap) + cell
	case Center:
		l := gap / 2
		return strings.Repeat(" ", l) + cell + strings.Repeat(" ", gap-l)
	}
	return cell + strings.Repeat(" ", gap)
}

// Table lays rows out in aligned columns separated by two spaces. With header, the first row is
// bold and underlined by a rule. Lines carry no trailing spaces.
func (s Style) Table(rows [][]string, aligns []Align, header bool) []string {
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for i, c := range r {
			if w := Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var lines []string
	for ri, r := range rows {
		cells := make([]string, cols)
		for i := range cols {
			c := ""
			if i < len(r) {
				c = r[i]
			}
			a := Left
			if i < len(aligns) {
				a = aligns[i]
			}
			cells[i] = pad(c, widths[i], a)
			if header && ri == 0 {
				cells[i] = s.Bold(cells[i])
			}
		}
		lines = append(lines, strings.TrimRight(strings.Join(cells, "  "), " "))
		if header && ri == 0 {
			rule := make([]string, cols)
			for i := range cols {
				rule[i] = strings.Repeat("─", widths[i])
			}
			lines = append(lines, s.Dim(strings.Join(rule, "  ")))
		}
	}
	return lines
}
