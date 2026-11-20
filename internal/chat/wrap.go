package chat

import "unicode/utf8"

// runeWidth is how many columns r takes: 0 for combining marks and joiners, 2 for wide
// characters (CJK, emoji), otherwise 1.
func runeWidth(r rune) int {
	switch {
	case r >= 0x0300 && r <= 0x036F, r >= 0x064B && r <= 0x065F, r >= 0x200B && r <= 0x200F,
		r >= 0xFE00 && r <= 0xFE0F, r == 0x0670, r >= 0x20D0 && r <= 0x20FF:
		return 0
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F300 && r <= 0x1FAFF, r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// csiEnd returns the length of the escape sequence at the start of s, 0 if there is none.
func csiEnd(s string) int {
	if len(s) < 2 || s[0] != 0x1b || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

// Width is the printed width of s in columns, ignoring ANSI sequences.
func Width(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if k := csiEnd(s[i:]); k > 0 {
			i += k
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n += runeWidth(r)
	}
	return n
}

// cutVisible shortens s to at most n columns, ending it with an ellipsis when it was cut. Escape
// sequences are kept whole.
func cutVisible(s string, n int) string {
	if s == "" || Width(s) <= n {
		return s
	}
	var out []byte
	w := 0
	for i := 0; i < len(s); {
		if k := csiEnd(s[i:]); k > 0 {
			out = append(out, s[i:i+k]...)
			i += k
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runeWidth(r)
		if w+rw > n-1 {
			break
		}
		w += rw
		out = append(out, s[i:i+size]...)
		i += size
	}
	return string(out) + "…\x1b[0m"
}

// breakRow cuts line, which is wider than avail columns, into the first row and the rest: at the
// last space that fits, or in the middle of a word when there is none.
func breakRow(line string, avail int) (row, rest string) {
	if avail < 8 {
		avail = 8
	}
	w, lastSpace, cut := 0, -1, len(line)
	i := 0
	for i < len(line) {
		if k := csiEnd(line[i:]); k > 0 {
			i += k
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		rw := runeWidth(r)
		if w+rw > avail {
			cut = i
			break
		}
		if r == ' ' && w > 0 {
			lastSpace = i
		}
		w += rw
		i += size
	}
	if lastSpace > 0 && lastSpace > indentLen(line) {
		return line[:lastSpace], line[lastSpace+1:]
	}
	if cut == 0 {
		cut = i
	}
	return line[:cut], line[cut:]
}

// indentLen is the number of leading spaces, after any escape sequences.
func indentLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if k := csiEnd(s[i:]); k > 0 {
			i += k
			continue
		}
		if s[i] != ' ' {
			break
		}
		n++
		i++
	}
	return n
}

// indentOf is the indent for the continuation rows of a line whose first row is row: the row's
// own indent, or two spaces under a label.
func indentOf(row string) string {
	if n := indentLen(row); n > 0 {
		if n > 8 {
			n = 8
		}
		return spaces(n)
	}
	return "  "
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
