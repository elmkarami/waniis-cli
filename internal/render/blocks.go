package render

import (
	"regexp"
	"strings"
)

var (
	headingStart = regexp.MustCompile(`^(#{1,6})(?:[ \t]+|$)`)
	bulletStart  = regexp.MustCompile(`^([-*+])[ \t]+`)
	orderedStart = regexp.MustCompile(`^(\d{1,9}[.)])[ \t]+`)
	quoteStart   = regexp.MustCompile(`^>[ \t]?`)
)

// ruleLine says whether t (no indent) is a thematic break: three or more of one of - * _,
// spaces allowed between.
func ruleLine(t string) bool {
	t = strings.TrimRight(t, " \t")
	if len(t) < 3 || !strings.ContainsRune("-*_", rune(t[0])) {
		return false
	}
	n := 0
	for _, r := range t {
		switch r {
		case rune(t[0]):
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

// lineReady says whether enough of a streaming line is known to tell how it starts: a space
// after the first word, so "##", "-", "1." or "---" aren't taken for text too early.
func lineReady(t string) bool {
	sp := strings.IndexAny(t, " \t")
	if sp < 0 {
		return false
	}
	// "- [" might still become a task box.
	if bulletStart.MatchString(t) {
		rest := bulletStart.ReplaceAllString(t, "")
		for _, box := range []string{"[ ] ", "[x] ", "[X] "} {
			if len(rest) < len(box) && strings.HasPrefix(box, rest) {
				return false
			}
		}
	}
	return true
}

// lineStart reads how an ordinary line starts: it returns what to print in place of its
// Markdown marker, how many bytes of the line that replaces, and sets up the line's inline
// styles. Until the start is known (a line still streaming) it reports !ok; with final the
// whole line is there.
func (m *MD) lineStart(line string, final bool) (prefix string, skip int, ok bool) {
	st := m.opts.Style
	t := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(t)]
	if !final && !lineReady(t) {
		return "", 0, false
	}
	m.inl = newInline()
	if len(indent) < 4 {
		if final && ruleLine(t) {
			w := 40
			if m.opts.Width > 0 && m.opts.Width < w {
				w = m.opts.Width
			}
			return st.Dim(strings.Repeat("─", w)), len(line), true
		}
		if g := headingStart.FindStringSubmatch(t); g != nil {
			if len(g[1]) == 1 {
				m.inl = newInline(sgrBold, sgrUnderline)
			} else {
				m.inl = newInline(sgrBold)
			}
			return m.inl.start(), len(indent) + len(g[0]), true
		}
		if g := quoteStart.FindString(t); g != "" {
			return indent + st.Dim("│") + " ", len(indent) + len(g), true
		}
	}
	if g := bulletStart.FindString(t); g != "" {
		rest := t[len(g):]
		mark := st.Dim("•")
		switch {
		case strings.HasPrefix(rest, "[ ] "):
			return indent + "☐ ", len(indent) + len(g) + 4, true
		case strings.HasPrefix(rest, "[x] "), strings.HasPrefix(rest, "[X] "):
			return indent + st.Green("☑") + " ", len(indent) + len(g) + 4, true
		}
		return indent + mark + " ", len(indent) + len(g), true
	}
	if g := orderedStart.FindStringSubmatch(t); g != nil {
		return indent + st.Dim(g[1]) + " ", len(indent) + len(g[0]), true
	}
	return "", 0, true
}
