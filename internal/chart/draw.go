package chart

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/elmkarami/waniis-cli/internal/style"
)

// MinPlotWidth is the narrowest terminal that gets a drawn chart; narrower ones get the table only.
const MinPlotWidth = 50

// Options say how to render a chart.
type Options struct {
	Style style.Style
	Width int  // terminal columns
	Draw  bool // draw the plot above the table
}

// eighths are the partial blocks, 1/8 to 7/8 of a cell.
var eighths = []rune("▏▎▍▌▋▊▉")

// fills mark series when colors are off, so series stay apart in a monochrome terminal.
var fills = []string{"█", "▓", "▒", "░"}

// Render returns the chart as terminal lines: the title, the plot when Draw and the terminal is
// wide enough, the table, and the source line.
func Render(c *Chart, o Options) []string {
	st := o.Style
	lines := []string{st.Bold(c.Title + HeadingSuffix(c))}
	if o.Draw && o.Width >= MinPlotWidth {
		var plot []string
		if c.Kind == "bar" || c.Kind == "stacked_bar" {
			plot = bars(c, st, o.Width)
		} else {
			plot = linePlot(c, st, o.Width)
		}
		if len(plot) > 0 {
			lines = append(lines, "")
			lines = append(lines, plot...)
			lines = append(lines, "")
		}
	}
	aligns := make([]style.Align, len(c.Series)+1)
	for i := 1; i < len(aligns); i++ {
		aligns[i] = style.Right
	}
	lines = append(lines, st.Table(Rows(c), aligns, true)...)
	if c.Source != "" {
		lines = append(lines, st.Dim("Source: "+c.Source))
	}
	return lines
}

func legend(c *Chart, st style.Style, marks []string) string {
	parts := make([]string, len(c.Series))
	for i, s := range c.Series {
		parts[i] = st.Series(i, marks[i%len(marks)]) + " " + s.Name
	}
	return strings.Join(parts, "   ")
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func barRun(st style.Style, series, cells int, rem int) string {
	if st.On || series == 0 {
		s := strings.Repeat("█", cells)
		if rem > 0 {
			s += string(eighths[rem-1])
		}
		return st.Series(series, s)
	}
	if rem >= 4 {
		cells++
	}
	return strings.Repeat(fills[series%len(fills)], cells)
}

func bars(c *Chart, st style.Style, width int) []string {
	labelW := 0
	for _, l := range c.X {
		if w := utf8.RuneCountInString(l); w > labelW {
			labelW = w
		}
	}
	if labelW > 16 {
		labelW = 16
	}
	stacked := c.Kind == "stacked_bar"
	valueW := 0
	var scale float64
	totals := make([]float64, len(c.X))
	for i := range c.X {
		for _, s := range c.Series {
			v := s.Values[i]
			totals[i] += v
			if w := utf8.RuneCountInString(FormatValue(c, v)); !stacked && w > valueW {
				valueW = w
			}
			if !stacked && math.Abs(v) > scale {
				scale = math.Abs(v)
			}
		}
		if stacked {
			if w := utf8.RuneCountInString(FormatValue(c, totals[i])); w > valueW {
				valueW = w
			}
			if totals[i] > scale {
				scale = totals[i]
			}
		}
	}
	barMax := width - labelW - valueW - 3
	if barMax < 10 || scale == 0 {
		return nil
	}
	var out []string
	if len(c.Series) > 1 {
		marks := make([]string, len(c.Series))
		for i := range marks {
			marks[i] = "█"
			if !st.On {
				marks[i] = fills[i%len(fills)]
			}
		}
		out = append(out, legend(c, st, marks), "")
	}
	units := func(v float64) int { return int(math.Round(math.Abs(v) / scale * float64(barMax) * 8)) }
	for i, label := range c.X {
		lab := trunc(label, labelW)
		pad := strings.Repeat(" ", labelW-utf8.RuneCountInString(lab))
		if stacked {
			var b strings.Builder
			cum, drawn := 0.0, 0
			for si, s := range c.Series {
				cum += s.Values[i]
				end := int(math.Round(cum / scale * float64(barMax)))
				if n := end - drawn; n > 0 {
					seg := strings.Repeat("█", n)
					if !st.On {
						seg = strings.Repeat(fills[si%len(fills)], n)
					}
					b.WriteString(st.Series(si, seg))
					drawn = end
				}
			}
			out = append(out, lab+pad+" "+b.String()+" "+FormatValue(c, totals[i]))
			continue
		}
		for si, s := range c.Series {
			u := units(s.Values[i])
			bar := barRun(st, si, u/8, u%8)
			prefix := strings.Repeat(" ", labelW)
			if si == 0 {
				prefix = lab + pad
			}
			out = append(out, prefix+" "+bar+" "+FormatValue(c, s.Values[i]))
		}
	}
	return out
}

type cell struct {
	r      rune
	series int
}

var markers = []rune("●◆■▲")

const plotHeight = 8

func linePlot(c *Chart, st style.Style, width int) []string {
	n := len(c.X)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range c.Series {
		for _, v := range s.Values {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	if c.Kind == "area" {
		lo = math.Min(lo, 0)
	}
	if hi == lo {
		hi = lo + 1
	}
	topL, botL := FormatValue(c, hi), FormatValue(c, lo)
	yW := max(utf8.RuneCountInString(topL), utf8.RuneCountInString(botL))
	avail := width - yW - 3
	if avail < 10 {
		return nil
	}
	step := 1
	if n > 1 {
		step = min(8, max(1, (avail-1)/(n-1)))
	}
	pw := (n-1)*step + 1
	grid := make([][]cell, plotHeight)
	for r := range grid {
		grid[r] = make([]cell, pw)
	}
	row := func(v float64) int { return int(math.Round((v - lo) / (hi - lo) * float64(plotHeight-1))) }
	for si, s := range c.Series {
		for i, v := range s.Values {
			col, r := i*step, row(v)
			if i+1 < n {
				r2 := row(s.Values[i+1])
				for k := 1; k < step; k++ {
					ir := r + int(math.Round(float64(r2-r)*float64(k)/float64(step)))
					if grid[ir][col+k].r == 0 {
						grid[ir][col+k] = cell{'·', si}
					}
				}
			}
			if c.Kind == "area" {
				for rr := 0; rr < r; rr++ {
					if grid[rr][col].r == 0 {
						grid[rr][col] = cell{'░', si}
					}
				}
			}
			grid[r][col] = cell{markers[si%len(markers)], si}
		}
	}
	var out []string
	if len(c.Series) > 1 {
		ms := make([]string, len(c.Series))
		for i := range ms {
			ms[i] = string(markers[i%len(markers)])
		}
		out = append(out, legend(c, st, ms), "")
	}
	for r := plotHeight - 1; r >= 0; r-- {
		label := ""
		switch r {
		case plotHeight - 1:
			label = topL
		case 0:
			label = botL
		}
		var b strings.Builder
		for _, ce := range grid[r] {
			switch {
			case ce.r == 0:
				b.WriteByte(' ')
			case ce.r == '·' || ce.r == '░':
				b.WriteString(st.Dim(string(ce.r)))
			default:
				b.WriteString(st.Series(ce.series, string(ce.r)))
			}
		}
		out = append(out, strings.Repeat(" ", yW-utf8.RuneCountInString(label))+label+" ┤"+strings.TrimRight(b.String(), " "))
	}
	out = append(out, strings.Repeat(" ", yW)+" └"+strings.Repeat("─", pw))
	first, last := c.X[0], c.X[n-1]
	axis := first
	if n > 1 {
		gap := pw - utf8.RuneCountInString(first) - utf8.RuneCountInString(last)
		if gap >= 1 {
			axis = first + strings.Repeat(" ", gap) + last
		}
	}
	out = append(out, strings.Repeat(" ", yW+2)+st.Dim(axis))
	return out
}
