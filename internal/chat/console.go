package chat

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// MaxMenu is how many commands the menu shows at once.
const MaxMenu = 6

// Console is the chat's output. In Raw mode (a terminal in raw input mode) it keeps a footer at
// the bottom: the half-written line of a streaming answer, the live status line, the input line
// and the command menu. Text written to it scrolls above the footer, which is drawn again after,
// so the input line stays usable while Waniis streams. Otherwise it is a plain writer.
type Console struct {
	Out    io.Writer
	Raw    bool
	Width  func() int
	Prompt func() string // the prompt text, plain
	Ed     *Editor
	Bold   func(string) string // styling for the selected menu item; nil: none
	Dim    func(string) string
	// Pick, when set, is drawn under the input line instead of the command menu and takes the
	// keys. Set and clear it with OpenPicker and inside Edit.
	Pick      *Picker
	PickStyle PickStyle

	mu      sync.Mutex
	partial string // the unfinished line of what was written, shown above the status line
	indent  string // continuation rows of the logical line being written start with this
	wrapped bool   // the logical line has already been cut into rows
	status  string // the status line, "" for none
	above   int    // footer rows above the input row on screen
	drawn   bool   // a footer is on screen
}

// Write prints p. In raw mode "\n" becomes "\r\n" (raw mode turns output translation off), long
// lines are cut into rows at spaces, finished rows scroll up and the footer is drawn again below;
// the unfinished line stays in the footer until it ends.
func (c *Console) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if !c.Raw {
		c.w(string(p))
		return len(p), nil
	}
	s := strings.ReplaceAll(strings.ReplaceAll(string(p), "\r\n", "\n"), "\r", "")
	c.clear()
	lines := strings.Split(c.partial+s, "\n")
	for _, l := range lines[:len(lines)-1] {
		c.emit(l, true)
		c.wrapped = false
	}
	c.partial = c.emit(lines[len(lines)-1], false)
	c.w(c.draw())
	c.drawn = true
	return len(p), nil
}

// limit is how many columns a row may use: one less than the terminal, so a full row never
// makes the terminal wrap by itself.
func (c *Console) limit() int { return c.width() - 1 }

// emit writes the rows of one line. For a finished line everything is written; otherwise only
// the rows that are full, and the rest is returned to stay in the footer.
func (c *Console) emit(line string, final bool) string {
	for {
		avail := c.limit()
		if Width(line) <= avail {
			break
		}
		row, rest := breakRow(line, avail)
		if !c.wrapped {
			c.indent, c.wrapped = indentOf(row), true
		}
		c.w(row + "\x1b[0m\r\n")
		line = c.indent + rest
	}
	if final {
		c.w(line + "\x1b[0m\r\n")
		return ""
	}
	return line
}

// clear removes the footer from the screen, leaving the cursor where it started.
func (c *Console) clear() {
	if !c.drawn {
		return
	}
	up := ""
	if c.above > 0 {
		up = fmt.Sprintf("\x1b[%dA", c.above)
	}
	c.w("\r" + up + "\x1b[J")
	c.drawn, c.above = false, 0
}

// Edit runs fn while holding the console's lock: the input line (Ed) is read when the footer is
// drawn, which can happen from the stream's goroutine at any time, so every change to it from the
// key-reading goroutine goes through here.
func (c *Console) Edit(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}

// SetStatus sets the live status line (already styled, no newline); "" removes it.
func (c *Console) SetStatus(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status == line {
		return
	}
	c.status = line
	if c.Raw {
		c.clear()
		c.w(c.draw())
		c.drawn = true
	}
}

// Redraw draws the footer again (after a key press).
func (c *Console) Redraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Raw {
		return
	}
	c.clear()
	c.w(c.draw())
	c.drawn = true
}

// Hide removes the footer, finishing any half-written line, and leaves the cursor at the start
// of an empty line.
func (c *Console) Hide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Raw {
		return
	}
	c.clear()
	c.status = ""
	if c.partial != "" {
		c.w(c.partial + "\x1b[0m\r\n")
		c.partial, c.wrapped = "", false
	}
}

// Clear wipes the screen.
func (c *Console) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Raw {
		c.drawn, c.above = false, 0
		c.w("\x1b[2J\x1b[H")
		c.w(c.draw())
		c.drawn = true
	}
}

func (c *Console) width() int {
	if c.Width != nil {
		if w := c.Width(); w > 20 {
			return w
		}
	}
	return 80
}

// draw is the input line plus the menu, with the cursor back on the input line.
func (c *Console) draw() string {
	w := c.width()
	prompt := c.Prompt()
	promptW := utf8.RuneCountInString(prompt)
	avail := max(8, w-promptW-1)
	buf, pos := c.Ed.Buf, c.Ed.Pos
	start := 0
	if len(buf) > avail {
		start = max(0, min(pos-avail+1, len(buf)-avail))
	}
	end := min(len(buf), start+avail)
	var b strings.Builder
	c.above = 0
	rows := []string{c.partial}
	if c.status != "" {
		for _, l := range strings.Split(c.status, "\n") {
			rows = append(rows, cutVisible(l, w-1))
		}
	}
	for _, row := range rows {
		if row != "" {
			b.WriteString(row + "\x1b[0m\r\n")
			c.above++
		}
	}
	b.WriteString(prompt + string(buf[start:end]))
	lines := c.menuLines(w)
	for _, l := range lines {
		b.WriteString("\r\n" + l)
	}
	if len(lines) > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", len(lines))
	}
	fmt.Fprintf(&b, "\r\x1b[%dC", promptW+pos-start)
	return b.String()
}

// OpenPicker shows p under the input line; it takes the keys until it is done.
func (c *Console) OpenPicker(p *Picker) {
	c.mu.Lock()
	c.Pick = p
	c.mu.Unlock()
	c.Redraw()
}

func (c *Console) menuLines(w int) []string {
	if c.Pick != nil {
		return c.Pick.Lines(w, c.PickStyle)
	}
	items := c.Ed.Menu()
	if len(items) == 0 {
		return nil
	}
	sel := c.Ed.Sel % len(items)
	from := 0
	if len(items) > MaxMenu {
		from = min(max(0, sel-MaxMenu/2), len(items)-MaxMenu)
		items = items[from : from+MaxMenu]
	}
	nameW := 0
	label := func(cmd Command) string { return strings.TrimSpace("/" + cmd.Name + " " + cmd.Args) }
	for _, it := range items {
		nameW = max(nameW, utf8.RuneCountInString(label(it)))
	}
	var out []string
	for i, it := range items {
		l := label(it)
		text := "  " + l + strings.Repeat(" ", nameW-utf8.RuneCountInString(l)+2) + it.Desc
		if r := []rune(text); len(r) > w-1 {
			text = string(r[:w-2]) + "…"
		}
		if from+i == sel {
			text = "›" + text[1:]
			if c.Bold != nil {
				text = c.Bold(text)
			}
		} else if c.Dim != nil {
			text = c.Dim(text)
		}
		out = append(out, text)
	}
	return out
}

func (c *Console) w(s string) { _, _ = io.WriteString(c.Out, s) }
