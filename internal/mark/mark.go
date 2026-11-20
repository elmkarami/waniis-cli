// Package mark draws the Waniis mark (the W, its loop and the dot) turning in 3D, in Braille
// characters, for a terminal's status line. The mark's lines are points in space: each frame
// turns them about the vertical axis, projects them with a little perspective and lights the
// nearest Braille dot, so the mark reads as a thin wire shape spinning like a coin.
package mark

import (
	"math"
	"strconv"
	"strings"
)

// The mark's shape, from the web app's SVG (apps/web/src/components/brand.tsx), in its units.
var (
	wPath = [][2]float64{{458, 710}, {730, 1222}, {1000, 710}, {1272, 1222}, {1461, 864}}
	loopC = [2]float64{1366, 816}
	loopR = 107.0
	dotC  = [2]float64{730, 816}
	// The bounds of the strokes' centre lines.
	minX, maxX = 458.0, 1473.0
	minY, maxY = 709.0, 1222.0
)

type point struct{ x, y float64 }

// outline is the mark's centre lines as points, every step units.
func outline(step float64) (lines, dot []point) {
	for i := 0; i+1 < len(wPath); i++ {
		a, b := wPath[i], wPath[i+1]
		n := int(math.Hypot(b[0]-a[0], b[1]-a[1]) / step)
		for k := 0; k <= n; k++ {
			t := float64(k) / float64(max(n, 1))
			lines = append(lines, point{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])})
		}
	}
	n := int(2 * math.Pi * loopR / step)
	for k := 0; k < n; k++ {
		th := float64(k) / float64(n) * 2 * math.Pi
		lines = append(lines, point{loopC[0] + loopR*math.Cos(th), loopC[1] + loopR*math.Sin(th)})
	}
	return lines, []point{{dotC[0], dotC[1]}}
}

// Frame is the mark turned by angle (radians) in cols×rows Braille cells. facing is cos(angle):
// 1 face on, 0 edge on, below 0 the back.
func Frame(angle float64, cols, rows int) (cells string, facing float64) {
	w, h := cols*2, rows*4
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	// Map the mark's height onto the grid's (one dot of margin when there is room), and its
	// width with Braille's aspect: a dot cell is about as wide as it is tall at 2×4 per cell.
	span := float64(h - 1)
	if rows > 1 {
		span = float64(h - 2)
	}
	scale := span / (maxY - minY)
	c, s := math.Cos(angle), math.Sin(angle)
	const persp = 3.2 // the eye's distance, in mark widths
	lines, dot := outline(6)
	grid := make([][]bool, h)
	for i := range grid {
		grid[i] = make([]bool, w)
	}
	plot := func(p point, fat bool) {
		x, y := p.x-cx, p.y-cy
		X, Z := x*c, x*s
		k := persp * (maxX - minX) / (persp*(maxX-minX) - Z)
		px := X*k*scale + float64(w)/2 - 0.5
		py := y*k*scale + float64(h)/2 - 0.5
		ix, iy := int(math.Round(px)), int(math.Round(py))
		set := func(ix, iy int) {
			if ix >= 0 && ix < w && iy >= 0 && iy < h {
				grid[iy][ix] = true
			}
		}
		set(ix, iy)
		if fat {
			set(ix+1, iy)
		}
	}
	for _, p := range lines {
		plot(p, false)
	}
	for _, p := range dot {
		plot(p, math.Abs(c) > 0.5)
	}
	var b strings.Builder
	bits := [4][2]int{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	for r := 0; r < rows; r++ {
		if r > 0 {
			b.WriteByte('\n')
		}
		for col := 0; col < cols; col++ {
			v := 0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					if grid[r*4+dy][col*2+dx] {
						v |= bits[dy][dx]
					}
				}
			}
			b.WriteRune(rune(0x2800 + v))
		}
	}
	return b.String(), c
}

// Cycle is how many frames one turn takes; the mark rests near face on for part of it.
const Cycle = 26

// rest is the pose the mark rests in: turned a little, so it reads as a shape in space.
const rest = -0.38

// AngleAt is the mark's angle at frame i: a smooth half-second-ish turn, then a rest.
func AngleAt(i int) float64 {
	const turning = 18
	f := i % Cycle
	if f >= turning {
		return rest
	}
	t := float64(f) / turning
	ease := t * t * (3 - 2*t)
	return rest + ease*2*math.Pi
}

// Color says how many colors the terminal shows.
type Color int

const (
	Color16 Color = iota
	Color256
	ColorTrue
)

// DetectColor reads the terminal's color support from the environment.
func DetectColor(getenv func(string) string) Color {
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return ColorTrue
	}
	if strings.Contains(getenv("TERM"), "256") {
		return Color256
	}
	return Color16
}

// shade colors the mark: the brand orange (#f0531c) face on, darker as it turns away, darkest
// on its back.
func shade(text string, facing float64, c Color) string {
	light := math.Abs(facing)
	if facing < 0 {
		light *= 0.6
	}
	v := 0.42 + 0.58*light
	switch c {
	case ColorTrue:
		return "\x1b[38;2;" + strconv.Itoa(int(240*v)) + ";" + strconv.Itoa(int(83*v)) + ";" + strconv.Itoa(int(28*v)) + "m" + text + "\x1b[0m"
	case Color256:
		codes := []string{"94", "130", "166", "202"}
		i := min(len(codes)-1, int(v*float64(len(codes))-0.7))
		return "\x1b[38;5;" + codes[max(0, i)] + "m" + text + "\x1b[0m"
	}
	if v > 0.7 {
		return "\x1b[33m" + text + "\x1b[0m"
	}
	return "\x1b[2;33m" + text + "\x1b[0m"
}

// Size is the spinner's size in cells: two lines of six.
const (
	Cols = 6
	Rows = 2
)

// Spinner draws the turning mark for an animation frame: Rows lines of Cols cells, colored.
// Frames are computed once and reused.
type Spinner struct {
	color  Color
	frames [][Rows]string
}

// NewSpinner computes every frame for a terminal with the given colors.
func NewSpinner(c Color) *Spinner {
	sp := &Spinner{color: c}
	for i := 0; i < Cycle; i++ {
		cells, facing := Frame(AngleAt(i), Cols, Rows)
		var f [Rows]string
		for r, l := range strings.SplitN(cells, "\n", Rows) {
			f[r] = shade(l, facing, c)
		}
		sp.frames = append(sp.frames, f)
	}
	return sp
}

// Lines is frame i, one string per line.
func (sp *Spinner) Lines(i int) [Rows]string { return sp.frames[((i%Cycle)+Cycle)%Cycle] }
