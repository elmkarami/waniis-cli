package chart

import (
	"math"
	"strconv"
	"strings"
)

var currencySymbols = map[string]string{"USD": "$", "EUR": "€", "GBP": "£", "JPY": "¥", "INR": "₹", "CNY": "CN¥", "CAD": "CA$", "AUD": "A$", "KRW": "₩", "BRL": "R$", "MXN": "MX$", "CHF": "CHF "}

// group puts thousands separators in the integer part of a plain decimal string.
func group(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if hasFrac {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// decimal formats v with at most max fraction digits and no trailing zeros, grouped (en-US).
func decimal(v float64, max int) string {
	s := strconv.FormatFloat(v, 'f', max, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return group(s)
}

// FormatValue renders a value as the web app does (en-US): "$12,400", "12.5%", "340 ms".
func FormatValue(c *Chart, v float64) string {
	switch c.Format {
	case "currency":
		digits := 2
		if v == math.Trunc(v) {
			digits = 0
		}
		s := strconv.FormatFloat(math.Abs(v), 'f', digits, 64)
		sym, ok := currencySymbols[c.Currency]
		if !ok {
			sym = c.Currency + " "
		}
		out := sym + group(s)
		if v < 0 {
			out = "-" + out
		}
		return out
	case "percent":
		return decimal(v, 1) + "%"
	}
	s := decimal(v, 2)
	if c.Unit != "" {
		s += " " + c.Unit
	}
	return s
}

// Rows are the table's cells: a header (blank, then the series), then one row per x label. The
// columns match the Slack bot's table.
func Rows(c *Chart) [][]string {
	rows := [][]string{append([]string{""}, seriesNames(c)...)}
	for i, label := range c.X {
		row := []string{label}
		for _, s := range c.Series {
			row = append(row, FormatValue(c, s.Values[i]))
		}
		rows = append(rows, row)
	}
	return rows
}

func seriesNames(c *Chart) []string {
	n := make([]string, len(c.Series))
	for i, s := range c.Series {
		n[i] = s.Name
	}
	return n
}

// HeadingSuffix is what follows the title: the currency code, or the plain unit.
func HeadingSuffix(c *Chart) string {
	switch {
	case c.Format == "currency" && c.Currency != "":
		return " (" + c.Currency + ")"
	case c.Format == "number" && c.Unit != "":
		return " (" + c.Unit + ")"
	}
	return ""
}
