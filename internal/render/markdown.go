package render

import (
	"io"
	"regexp"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/chart"
	"github.com/elmkarami/waniis-cli/internal/style"
)

// MDOptions say how an answer's Markdown is shown in a terminal.
type MDOptions struct {
	Style style.Style
	Width int  // terminal columns, for plots
	Draw  bool // draw charts above their tables
}

// indenter prefixes the first line it writes with first and every later line with rest.
type indenter struct {
	w            io.Writer
	first, rest  string
	started, mid bool
}

func (i *indenter) Write(p []byte) (int, error) {
	s := string(p)
	for len(s) > 0 {
		if !i.mid {
			prefix := i.rest
			if !i.started {
				prefix = i.first
			}
			if _, err := io.WriteString(i.w, prefix); err != nil {
				return 0, err
			}
			i.started, i.mid = true, true
		}
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			if _, err := io.WriteString(i.w, s); err != nil {
				return 0, err
			}
			break
		}
		if _, err := io.WriteString(i.w, s[:nl+1]); err != nil {
			return 0, err
		}
		i.mid = false
		s = s[nl+1:]
	}
	return len(p), nil
}

type mdMode int

const (
	mdNormal mdMode = iota
	mdPlainFence
	mdChartFence
	mdTable
)

// MD turns streamed Markdown into terminal text: charts become a plot and a table, Markdown
// tables become aligned tables. With colors on, the rest is styled too: headings, **bold**,
// *italic*, `code`, links, lists, quotes and rules, and code blocks are syntax highlighted;
// without colors it passes through as written. Text can arrive in any chunking; ordinary lines
// are written as soon as their first word is known, so a streamed answer reads live.
type MD struct {
	w    io.Writer
	opts MDOptions

	line    string // the current line so far
	printed int    // bytes of line already written
	mode    mdMode
	held    []string // raw lines of a chart fence or table being collected
	marker  string   // the open fence's marker
	started bool     // the current line is being written as it streams
	inl     inline   // the current line's inline styles
	hl      *highlighter
}

func (m *MD) styled() bool { return m.opts.Style.On }

// NewMD writes to w.
func NewMD(w io.Writer, o MDOptions) *MD { return &MD{w: w, opts: o} }

// Open reports whether a line is half written.
func (m *MD) Open() bool { return m.line != "" || len(m.held) > 0 }

func (m *MD) out(s string) { _, _ = io.WriteString(m.w, s) }

// Write adds a chunk of text.
func (m *MD) Write(s string) {
	for {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			m.line += s
			m.stream()
			return
		}
		m.line += s[:nl]
		s = s[nl+1:]
		m.endLine()
	}
}

// canStream says whether the current partial line can be shown before its line ends.
func (m *MD) canStream() bool {
	switch m.mode {
	case mdPlainFence:
		return !m.styled()
	case mdNormal:
		t := strings.TrimLeft(m.line, " ")
		return t != "" && !strings.ContainsAny(t[:1], "`~|")
	}
	return false
}

func (m *MD) stream() {
	if !m.canStream() || m.printed >= len(m.line) {
		return
	}
	if !m.styled() {
		m.out(m.line[m.printed:])
		m.printed, m.started = len(m.line), true
		return
	}
	if !m.started {
		prefix, skip, ok := m.lineStart(m.line, false)
		if !ok {
			return
		}
		m.out(prefix)
		m.printed, m.started = skip, true
	}
	m.out(m.inl.feed(m.line[m.printed:], false))
	m.printed = len(m.line)
}

func (m *MD) endLine() {
	line := m.line
	streamed := m.started
	if streamed {
		m.out(m.finishStreamed(line[m.printed:]) + "\n")
	}
	m.line, m.printed, m.started = "", 0, false
	if streamed {
		// A streamed line was ordinary text, or a line inside a plain fence: it can't open or
		// close anything, except the closing fence of a plain one.
		if m.mode == mdPlainFence {
			if mk, ok := closingFenceLine(line); ok && mk[0] == m.marker[0] && len(mk) >= len(m.marker) {
				m.mode = mdNormal
			}
		}
		return
	}
	m.process(line)
}

// finishStreamed is the end of a line that was streamed.
func (m *MD) finishStreamed(rest string) string {
	if !m.styled() || m.mode != mdNormal {
		return rest
	}
	return m.inl.feed(rest, true)
}

func closingFenceLine(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return "", false
	}
	if strings.Trim(t, string(t[0])) != "" {
		return "", false
	}
	return t, true
}

var tableRow = regexp.MustCompile(`^\s{0,3}\|`)

func (m *MD) process(line string) {
	switch m.mode {
	case mdPlainFence:
		if mk, ok := closingFenceLine(line); ok && mk[0] == m.marker[0] && len(mk) >= len(m.marker) {
			m.mode, m.hl = mdNormal, nil
			m.out(m.opts.Style.Dim(line) + "\n")
			return
		}
		if m.hl != nil {
			line = m.hl.line(line)
		}
		m.out(line + "\n")
		return
	case mdChartFence:
		m.held = append(m.held, line)
		if mk, ok := closingFenceLine(line); ok && mk[0] == m.marker[0] && len(mk) >= len(m.marker) {
			m.finishChart()
		}
		return
	case mdTable:
		if tableRow.MatchString(line) {
			m.held = append(m.held, line)
			return
		}
		m.finishTable()
	}
	if mk, lang, ok := openFenceLine(line); ok {
		m.marker = mk
		if lang == chart.FenceLanguage {
			m.mode, m.held = mdChartFence, []string{line}
			return
		}
		m.mode = mdPlainFence
		m.hl = newHighlighter(lang, m.opts.Style)
		m.out(m.opts.Style.Dim(line) + "\n")
		return
	}
	if tableRow.MatchString(line) {
		m.mode, m.held = mdTable, []string{line}
		return
	}
	m.out(m.formatLine(line) + "\n")
}

// formatLine styles a whole ordinary line.
func (m *MD) formatLine(line string) string {
	if !m.styled() {
		return line
	}
	prefix, skip, _ := m.lineStart(line, true)
	return prefix + m.inl.feed(line[skip:], true)
}

func openFenceLine(line string) (marker, lang string, ok bool) {
	f := chart.Fences(line + "\n")
	if len(f) != 1 || f[0].Closed {
		return "", "", false
	}
	t := strings.TrimLeft(line, " ")
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	return t[:n], f[0].Language, true
}

// Flush finishes the text: an unfinished block is printed as written, and an open line ends.
func (m *MD) Flush() {
	if m.line != "" {
		line, streamed := m.line, m.started
		rest := line[m.printed:]
		m.line, m.printed, m.started = "", 0, false
		if streamed {
			m.out(m.finishStreamed(rest) + "\n")
		} else {
			m.process(line)
		}
	}
	switch m.mode {
	case mdChartFence:
		for _, l := range m.held {
			m.out(l + "\n")
		}
		m.held = nil
	case mdTable:
		m.finishTable()
	}
	m.mode, m.hl = mdNormal, nil
}

func (m *MD) finishChart() {
	held := m.held
	m.held, m.mode = nil, mdNormal
	body := strings.Join(held[1:len(held)-1], "\n")
	c, err := chart.Parse(body)
	if err != nil {
		for _, l := range held {
			m.out(l + "\n")
		}
		return
	}
	for _, l := range chart.Render(c, chart.Options{Style: m.opts.Style, Width: m.opts.Width, Draw: m.opts.Draw}) {
		m.out(l + "\n")
	}
}

var (
	separatorCell = regexp.MustCompile(`^:?-+:?$`)
	numberish     = regexp.MustCompile(`^[-+(]?[$€£¥]?[\d][\d,._]*[%)]?(\s?[A-Za-z]{1,3})?$`)
)

func splitRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	var cells []string
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		switch {
		case t[i] == '\\' && i+1 < len(t) && t[i+1] == '|':
			b.WriteByte('|')
			i++
		case t[i] == '|':
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(t[i])
		}
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func (m *MD) finishTable() {
	held := m.held
	m.held, m.mode = nil, mdNormal
	raw := func() {
		for _, l := range held {
			m.out(l + "\n")
		}
	}
	if len(held) < 2 {
		raw()
		return
	}
	sep := splitRow(held[1])
	for _, c := range sep {
		if !separatorCell.MatchString(c) {
			raw()
			return
		}
	}
	rows := [][]string{splitRow(held[0])}
	for _, l := range held[2:] {
		rows = append(rows, splitRow(l))
	}
	cols := len(rows[0])
	aligns := make([]style.Align, cols)
	for i := range cols {
		explicit := i < len(sep) && (strings.HasPrefix(sep[i], ":") || strings.HasSuffix(sep[i], ":"))
		switch {
		case i < len(sep) && strings.HasPrefix(sep[i], ":") && strings.HasSuffix(sep[i], ":"):
			aligns[i] = style.Center
		case i < len(sep) && strings.HasSuffix(sep[i], ":"):
			aligns[i] = style.Right
		case !explicit && columnIsNumeric(rows[1:], i):
			aligns[i] = style.Right
		}
	}
	for _, r := range rows {
		for i := range r {
			r[i] = styleInline(m.opts.Style, r[i])
		}
	}
	for _, l := range m.opts.Style.Table(rows, aligns, true) {
		m.out(l + "\n")
	}
}

func columnIsNumeric(rows [][]string, col int) bool {
	seen := false
	for _, r := range rows {
		if col >= len(r) || r[col] == "" {
			continue
		}
		if !numberish.MatchString(r[col]) {
			return false
		}
		seen = true
	}
	return seen
}

// FormatMarkdown renders a whole answer at once.
func FormatMarkdown(text string, o MDOptions) string {
	var b strings.Builder
	m := NewMD(&b, o)
	m.Write(text)
	m.Flush()
	return b.String()
}
