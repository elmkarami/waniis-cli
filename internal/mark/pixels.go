package mark

import (
	"math"
	"strings"
)

// The SVG's view box.
const boxMinX, boxMinY, boxW, boxH = 380.0, 630.0, 1172.0, 672.0

// Ink is what a pixel of the mark is: nothing, a stroke (the W and its loop) or the dot.
type Ink int

const (
	Blank Ink = iota
	Stroke
	Dot
)

// Pixels draws the mark as crisp pixel art in a w×h grid: each stroke of the W two pixels wide
// on every row, the loop a one-pixel ring and the dot a 2×2 block in the first V, placed from the
// SVG's geometry (the view box fitted, centred).
func Pixels(w, h int) [][]Ink {
	sx := boxW / float64(w)
	sy := boxH / float64(h)
	X := func(x float64) float64 { return (x - boxMinX) / sx }
	Y := func(y float64) float64 { return (y - boxMinY) / sy }
	out := make([][]Ink, h)
	for i := range out {
		out[i] = make([]Ink, w)
	}
	set := func(x, y int, ink Ink) {
		if x >= 0 && x < w && y >= 0 && y < h && out[y][x] != Dot {
			out[y][x] = ink
		}
	}
	// The W: for each row a segment crosses, two pixels around where its centre line is.
	for i := 0; i+1 < len(wPath); i++ {
		ax, ay := X(wPath[i][0]), Y(wPath[i][1])
		bx, by := X(wPath[i+1][0]), Y(wPath[i+1][1])
		lo, hi := math.Min(ay, by), math.Max(ay, by)
		for row := 0; row < h; row++ {
			cy := float64(row) + 0.5
			if cy < lo-0.5 || cy > hi+0.5 {
				continue
			}
			t := (math.Max(lo, math.Min(hi, cy)) - ay) / (by - ay)
			cx := ax + t*(bx-ax)
			left := int(math.Floor(cx - 0.5))
			set(left, row, Stroke)
			set(left+1, row, Stroke)
		}
	}
	// The loop: a ring around its centre.
	cx, cy := X(loopC[0]), Y(loopC[1])
	rx, ry := loopR/sx, loopR/sy
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			dx := (float64(col) + 0.5 - cx) / rx
			dy := (float64(row) + 0.5 - cy) / ry
			if d := math.Hypot(dx, dy); d > 0.55 && d < 1.45 {
				set(col, row, Stroke)
			}
		}
	}
	// The dot: 2×2, centred on its place.
	dx, dy := int(math.Round(X(dotC[0])-1)), int(math.Round(Y(dotC[1])-1))
	for y := dy; y < dy+2; y++ {
		for x := dx; x < dx+2; x++ {
			if x >= 0 && x < w && y >= 0 && y < h {
				out[y][x] = Dot
			}
		}
	}
	return out
}

// Logo is the mark as half-block pixel art, two pixels per character cell, h pixels tall (even):
// strokes in the terminal's own text color, the dot in the brand's orange. Without colors the
// dot is drawn like a stroke.
func Logo(w, h int, color bool, c Color) []string {
	px := Pixels(w, h)
	dot := func(fg bool) string {
		if !color {
			return ""
		}
		switch c {
		case ColorTrue:
			if fg {
				return "\x1b[38;2;240;83;28m"
			}
			return "\x1b[48;2;240;83;28m"
		case Color256:
			if fg {
				return "\x1b[38;5;202m"
			}
			return "\x1b[48;5;202m"
		}
		if fg {
			return "\x1b[33m"
		}
		return "\x1b[43m"
	}
	var lines []string
	for r := 0; r+1 < h; r += 2 {
		var b strings.Builder
		for x := 0; x < w; x++ {
			top, bot := px[r][x], px[r+1][x]
			if !color {
				if top == Dot {
					top = Stroke
				}
				if bot == Dot {
					bot = Stroke
				}
			}
			switch {
			case top == Blank && bot == Blank:
				b.WriteByte(' ')
			case top == bot:
				if top == Dot {
					b.WriteString(dot(true) + "█\x1b[0m")
				} else {
					b.WriteString("█")
				}
			case bot == Blank:
				if top == Dot {
					b.WriteString(dot(true) + "▀\x1b[0m")
				} else {
					b.WriteString("▀")
				}
			case top == Blank:
				if bot == Dot {
					b.WriteString(dot(true) + "▄\x1b[0m")
				} else {
					b.WriteString("▄")
				}
			case top == Dot: // the dot above a stroke: the dot as the background, the stroke as ▄
				if color {
					b.WriteString(dot(false) + "▄\x1b[0m")
				} else {
					b.WriteString("█")
				}
			default: // a stroke above the dot
				if color {
					b.WriteString(dot(false) + "▀\x1b[0m")
				} else {
					b.WriteString("█")
				}
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return lines
}
