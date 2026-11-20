// Package termtest is a tiny terminal emulator for tests: it plays the bytes the chat writes
// (carriage returns, newlines, cursor up and right, clearing, colors) onto a screen of rows, so a
// test can assert what a person would see, and where the cursor is.
package termtest

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Term is a screen of a fixed width. Colors and other styles are ignored.
type Term struct {
	Width int
	rows  [][]rune
	R, C  int // the cursor
	rest  string
}

// New returns an empty screen of the given width.
func New(width int) *Term { return &Term{Width: width, rows: [][]rune{{}}} }

func (t *Term) row(r int) []rune {
	for len(t.rows) <= r {
		t.rows = append(t.rows, nil)
	}
	return t.rows[r]
}

func (t *Term) put(r rune) {
	if t.C >= t.Width {
		t.R++
		t.C = 0
	}
	row := t.row(t.R)
	for len(row) < t.C {
		row = append(row, ' ')
	}
	if t.C < len(row) {
		row[t.C] = r
	} else {
		row = append(row, r)
	}
	t.rows[t.R] = row
	t.C++
}

// Write plays bytes onto the screen.
func (t *Term) Write(p []byte) (int, error) {
	s := t.rest + string(p)
	t.rest = ""
	for len(s) > 0 {
		switch s[0] {
		case '\r':
			t.C = 0
			s = s[1:]
		case '\n':
			t.R++
			t.row(t.R)
			s = s[1:]
		case 0x1b:
			if len(s) < 2 {
				t.rest = s
				return len(p), nil
			}
			if s[1] != '[' {
				s = s[2:]
				continue
			}
			end := 2
			for end < len(s) && (s[end] < 0x40 || s[end] > 0x7e) {
				end++
			}
			if end >= len(s) {
				t.rest = s
				return len(p), nil
			}
			t.csi(s[2:end], s[end])
			s = s[end+1:]
		default:
			r, size := utf8.DecodeRuneInString(s)
			if !utf8.FullRuneInString(s) {
				t.rest = s
				return len(p), nil
			}
			t.put(r)
			s = s[size:]
		}
	}
	return len(p), nil
}

func (t *Term) csi(params string, final byte) {
	n := 1
	if v, err := strconv.Atoi(params); err == nil {
		n = v
	}
	switch final {
	case 'A':
		t.R = max(0, t.R-n)
	case 'B':
		t.R += n
		t.row(t.R)
	case 'C':
		t.C += n
	case 'J':
		if params == "2" {
			t.rows, t.R, t.C = [][]rune{{}}, 0, 0
			return
		}
		row := t.row(t.R)
		if t.C < len(row) {
			t.rows[t.R] = row[:t.C]
		}
		t.rows = t.rows[:t.R+1]
	case 'H':
		t.R, t.C = 0, 0
	}
}

// Lines are the screen's rows without trailing spaces; empty rows at the end are dropped.
func (t *Term) Lines() []string {
	var out []string
	for _, r := range t.rows {
		out = append(out, strings.TrimRight(string(r), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// CursorLine is the text of the row the cursor is on.
func (t *Term) CursorLine() string {
	if t.R < len(t.rows) {
		return strings.TrimRight(string(t.rows[t.R]), " ")
	}
	return ""
}

// Play runs a whole output through a fresh screen.
func Play(width int, out string) *Term {
	t := New(width)
	_, _ = t.Write([]byte(out))
	return t
}
