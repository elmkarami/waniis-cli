package render

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elmkarami/waniis-cli/internal/style"
)

// SGR codes the inline styler uses.
const (
	sgrBold      = "1"
	sgrDim       = "2"
	sgrItalic    = "3"
	sgrUnderline = "4"
	sgrStrike    = "9"
	sgrCode      = "36"
)

// maxLinkHold is how much of a possible link ("[text](url)") is held back while it streams in;
// past it, the bracket is shown as written.
const maxLinkHold = 400

// inline styles one line of Markdown text as it streams in: **bold**, *italic*, ~~struck~~,
// `code` and [links](url). Markers are dropped and their text styled. Where a marker can't be
// told apart from plain text yet (a lone "*" at the end of a chunk, an unfinished link), that tail
// is held until the next chunk or the end of the line. It is only used with colors on.
type inline struct {
	base                 []string // codes for the whole line (a heading's bold)
	bold, italic, strike bool
	code                 int    // the open code span's backtick count; 0 when none
	pending              string // a held tail, not shown yet
	prev                 rune   // the last character shown, ' ' at the start
	dirty                bool   // some code was written since the line began
}

func newInline(base ...string) inline { return inline{base: base, prev: ' '} }

// sgr resets and turns on every style that applies now.
func (in *inline) sgr() string {
	codes := append([]string(nil), in.base...)
	if in.bold {
		codes = append(codes, sgrBold)
	}
	if in.italic {
		codes = append(codes, sgrItalic)
	}
	if in.strike {
		codes = append(codes, sgrStrike)
	}
	if in.code > 0 {
		codes = append(codes, sgrCode)
	}
	in.dirty = true
	if len(codes) == 0 {
		return "\x1b[0m"
	}
	return "\x1b[0m\x1b[" + strings.Join(codes, ";") + "m"
}

// start opens the line's base style.
func (in *inline) start() string {
	if len(in.base) == 0 {
		return ""
	}
	return in.sgr()
}

func runOf(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || unicode.IsSpace(r) }

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

const escapable = "\\`*_{}[]()#+-.!|~<>\"'"

// feed styles text, the next part of the line. With final, the line ends: everything held is
// settled and every style is closed.
func (in *inline) feed(text string, final bool) string {
	src := in.pending + text
	in.pending = ""
	var b strings.Builder
	hold := func(i int) string {
		in.pending = src[i:]
		return b.String()
	}
	literal := func(s string) {
		b.WriteString(s)
		if r, _ := utf8.DecodeLastRuneInString(s); r != utf8.RuneError {
			in.prev = r
		}
	}
	i := 0
	for i < len(src) {
		c := src[i]
		if in.code > 0 {
			if c != '`' {
				_, size := utf8.DecodeRuneInString(src[i:])
				literal(src[i : i+size])
				i += size
				continue
			}
			n := runOf(src, i, '`')
			if i+n == len(src) && !final {
				return hold(i)
			}
			if n == in.code {
				in.code = 0
				b.WriteString(in.sgr())
			} else {
				literal(src[i : i+n])
			}
			i += n
			continue
		}
		switch c {
		case '\\':
			if i+1 == len(src) {
				if !final {
					return hold(i)
				}
				literal("\\")
				i++
				continue
			}
			if strings.IndexByte(escapable, src[i+1]) >= 0 {
				literal(src[i+1 : i+2])
				i += 2
				continue
			}
		case '`':
			n := runOf(src, i, '`')
			if i+n == len(src) && !final {
				return hold(i)
			}
			if i+n == len(src) {
				literal(src[i:])
				i += n
				continue
			}
			in.code = n
			b.WriteString(in.sgr())
			i += n
			continue
		case '*', '_', '~':
			n := runOf(src, i, c)
			if i+n == len(src) && !final {
				return hold(i)
			}
			next := ' '
			if i+n < len(src) {
				next, _ = utf8.DecodeRuneInString(src[i+n:])
			}
			if in.emphasis(c, n, next, &b) {
				i += n
				continue
			}
			literal(src[i : i+n])
			i += n
			continue
		case '[':
			if text, url, size, ok := parseLink(src[i:]); ok {
				b.WriteString("\x1b[" + sgrUnderline + "m")
				literal(text)
				b.WriteString(in.sgr())
				if url != text {
					b.WriteString(" \x1b[" + sgrDim + "m(" + url + ")")
					b.WriteString(in.sgr())
				}
				i += size
				continue
			}
			if !final && len(src)-i < maxLinkHold && maybeLink(src[i:]) {
				return hold(i)
			}
		}
		_, size := utf8.DecodeRuneInString(src[i:])
		literal(src[i : i+size])
		i += size
	}
	if final {
		if in.dirty || in.bold || in.italic || in.strike || in.code > 0 || len(in.base) > 0 {
			b.WriteString("\x1b[0m")
		}
		*in = newInline(in.base...)
	}
	return b.String()
}

// emphasis turns a run of n markers c into a style change when it opens or closes one, and
// reports whether it did. A run opens before a non-space and closes after one; an underscore
// inside a word (snake_case) is text.
func (in *inline) emphasis(c byte, n int, next rune, b *strings.Builder) bool {
	canClose := !isSpace(in.prev)
	canOpen := !isSpace(next)
	if c == '_' && isWordRune(in.prev) && isWordRune(next) {
		return false
	}
	toggle := func(on *bool) bool {
		switch {
		case *on && canClose:
			*on = false
		case !*on && canOpen:
			*on = true
		default:
			return false
		}
		return true
	}
	changed := false
	switch {
	case c == '~':
		if n != 2 {
			return false
		}
		changed = toggle(&in.strike)
	case n == 1:
		changed = toggle(&in.italic)
	case n == 2:
		changed = toggle(&in.bold)
	case n == 3:
		// ***both***
		switch {
		case in.bold && in.italic && canClose:
			in.bold, in.italic, changed = false, false, true
		case !in.bold && !in.italic && canOpen:
			in.bold, in.italic, changed = true, true, true
		}
	}
	if changed {
		b.WriteString(in.sgr())
	}
	return changed
}

// parseLink reads "[text](url)" at the start of s.
func parseLink(s string) (text, url string, size int, ok bool) {
	close := strings.Index(s, "](")
	if close < 1 || strings.ContainsAny(s[1:close], "[]") {
		return "", "", 0, false
	}
	end := strings.IndexByte(s[close+2:], ')')
	if end < 1 {
		return "", "", 0, false
	}
	url = s[close+2 : close+2+end]
	if strings.ContainsAny(url, " \t") {
		return "", "", 0, false
	}
	return s[1:close], url, close + 3 + end, true
}

// maybeLink says whether s, starting with "[", could still become a link as more text arrives.
func maybeLink(s string) bool {
	close := strings.IndexByte(s, ']')
	if close < 0 {
		return !strings.ContainsRune(s[1:], '[')
	}
	if close == len(s)-1 {
		return true
	}
	if s[close+1] != '(' {
		return false
	}
	rest := s[close+2:]
	return !strings.ContainsAny(rest, ") \t")
}

// styleInline styles a whole piece of text at once (a table cell).
func styleInline(s style.Style, text string) string {
	if !s.On {
		return text
	}
	in := newInline()
	return in.feed(text, true)
}
