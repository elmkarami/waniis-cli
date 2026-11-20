package mark

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFrameFillsTheGrid(t *testing.T) {
	cells, facing := Frame(0, Cols, Rows)
	lines := strings.Split(cells, "\n")
	if len(lines) != Rows || facing != 1 {
		t.Fatalf("%q %v", cells, facing)
	}
	for _, l := range lines {
		if utf8.RuneCountInString(l) != Cols {
			t.Fatalf("line %q", l)
		}
	}
	// Face on, the mark is wide; edge on, it is a sliver in the middle.
	if lit(cells) < 20 {
		t.Fatalf("face on too faint: %q", cells)
	}
	edge, _ := Frame(math.Pi/2, Cols, Rows)
	if lit(edge) >= lit(cells)/2 {
		t.Fatalf("edge on should be thin: %q", edge)
	}
	for _, l := range strings.Split(edge, "\n") {
		r := []rune(l)
		if r[0] != 0x2800 || r[Cols-1] != 0x2800 {
			t.Fatalf("edge on reaches the sides: %q", edge)
		}
	}
}

// The back is the front mirrored: the W's loop moves to the other side.
func TestFrameBackIsMirrored(t *testing.T) {
	front, _ := Frame(0, Cols, Rows)
	back, facing := Frame(math.Pi, Cols, Rows)
	if facing > -0.99 || front == back {
		t.Fatalf("front %q back %q", front, back)
	}
}

func lit(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28ff {
			for v := r - 0x2800; v > 0; v &= v - 1 {
				n++
			}
		}
	}
	return n
}

func TestSpinnerTurnsAndRests(t *testing.T) {
	sp := NewSpinner(ColorTrue)
	if sp.Lines(0) == sp.Lines(5) {
		t.Fatal("frames should differ while turning")
	}
	if sp.Lines(Cycle-1) != sp.Lines(Cycle-2) {
		t.Fatal("it rests at the end of a turn")
	}
	if sp.Lines(Cycle+3) != sp.Lines(3) || sp.Lines(-1) != sp.Lines(Cycle-1) {
		t.Fatal("frames wrap")
	}
	if !strings.HasPrefix(sp.Lines(Cycle - 1)[0], "\x1b[38;2;") {
		t.Fatalf("true color: %q", sp.Lines(0)[0])
	}
	if !strings.HasPrefix(NewSpinner(Color256).Lines(0)[0], "\x1b[38;5;") {
		t.Fatal("256 colors")
	}
}

func TestDetectColor(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if DetectColor(env(map[string]string{"COLORTERM": "truecolor"})) != ColorTrue {
		t.Fatal("truecolor")
	}
	if DetectColor(env(map[string]string{"TERM": "xterm-256color"})) != Color256 {
		t.Fatal("256")
	}
	if DetectColor(env(nil)) != Color16 {
		t.Fatal("16")
	}
}
