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

// Console is the chat's output. In Raw mode (a terminal in raw input mode) it keeps the input
// line and the command menu at the bottom: text written to it appears above them, and they are
// drawn again after. Otherwise it is a plain writer.
type Console struct {
	Out    io.Writer
	Raw    bool
	Width  func() int
	Prompt func() string // the prompt text, plain
	Ed     *Editor
	Bold   func(string) string // styling for the selected menu item; nil: none
	Dim    func(string) string

	mu  sync.Mutex
	mid bool // the last write stopped in the middle of a line
}

// Write prints p. In raw mode "\n" becomes "\r\n" (raw mode turns output translation off), and
// the input area is cleared before and redrawn after a finished line.
func (c *Console) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	s := string(p)
	if c.Raw {
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
		if !c.mid {
			c.w("\r\x1b[J")
		}
	}
	c.w(s)
	c.mid = !strings.HasSuffix(string(p), "\n")
	if c.Raw && !c.mid {
		c.w(c.draw())
	}
	return len(p), nil
}

// Redraw draws the input line and menu again (after a key press).
func (c *Console) Redraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Raw || c.mid {
		return
	}
	c.w("\r\x1b[J" + c.draw())
}

// Hide clears the input area, leaving the cursor at the start of an empty line.
func (c *Console) Hide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Raw {
		c.w("\r\x1b[J")
	}
}

// Clear wipes the screen.
func (c *Console) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Raw {
		c.w("\x1b[2J\x1b[H" + c.draw())
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

func (c *Console) menuLines(w int) []string {
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
