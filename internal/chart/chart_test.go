package chart

import (
	"strings"
	"testing"
)

const ok = `{"kind":"bar","title":"T","x":["a","b"],"series":[{"name":"S","values":[1,2.5]}]}`

func TestParseAccepts(t *testing.T) {
	c, err := Parse(ok)
	if err != nil || c.Format != "number" || len(c.Series[0].Values) != 2 {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"bad json":       `{`,
		"unknown kind":   strings.Replace(ok, `"bar"`, `"pie"`, 1),
		"string value":   strings.Replace(ok, `[1,2.5]`, `["1",2]`, 1),
		"null value":     strings.Replace(ok, `[1,2.5]`, `[null,2]`, 1),
		"length":         strings.Replace(ok, `[1,2.5]`, `[1]`, 1),
		"empty title":    strings.Replace(ok, `"T"`, `"  "`, 1),
		"dup series":     strings.Replace(ok, `"series":[{"name":"S","values":[1,2.5]}]`, `"series":[{"name":"S","values":[1,2]},{"name":"s","values":[1,2]}]`, 1),
		"currency":       strings.Replace(ok, `"x"`, `"format":"currency","x"`, 1),
		"bad currency":   strings.Replace(ok, `"x"`, `"format":"currency","currency":"usd","x"`, 1),
		"stacked neg":    strings.Replace(strings.Replace(ok, `"bar"`, `"stacked_bar"`, 1), `[1,2.5]`, `[-1,2]`, 1),
		"too many x":     `{"kind":"bar","title":"T","x":["` + strings.Repeat(`a","`, 24) + `a"],"series":[{"name":"S","values":[1]}]}`,
		"magnitude":      strings.Replace(ok, `[1,2.5]`, `[1e16,2]`, 1),
		"too large":      ok + strings.Repeat(" ", MaxJSONChars),
		"long label":     strings.Replace(ok, `"a"`, `"`+strings.Repeat("x", 33)+`"`, 1),
		"unknown format": strings.Replace(ok, `"x"`, `"format":"hex","x"`, 1),
	}
	for name, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFormatValue(t *testing.T) {
	cur := &Chart{Format: "currency", Currency: "USD"}
	pct := &Chart{Format: "percent"}
	num := &Chart{Format: "number", Unit: "ms"}
	for _, c := range []struct {
		c    *Chart
		v    float64
		want string
	}{
		{cur, 12400, "$12,400"}, {cur, 12.5, "$12.50"}, {cur, -3, "-$3"},
		{pct, 12.5, "12.5%"}, {pct, 12, "12%"},
		{num, 340, "340 ms"}, {num, 1234.567, "1,234.57 ms"},
		{&Chart{Format: "currency", Currency: "MAD"}, 5, "MAD 5"},
	} {
		if got := FormatValue(c.c, c.v); got != c.want {
			t.Errorf("%v: %q want %q", c.v, got, c.want)
		}
	}
}

func TestFences(t *testing.T) {
	f := Fences("a\n```chart\n{}\n```\nb\n~~~go\nx\n")
	if len(f) != 2 || f[0].Language != "chart" || !f[0].Closed || f[0].Code != "{}" || f[1].Closed || f[1].Language != "go" {
		t.Fatalf("%+v", f)
	}
}
