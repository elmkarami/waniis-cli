package chat

import "strings"

// Result is what handling a key produced.
type Result int

const (
	None      Result = iota // nothing to act on; redraw
	Submit                  // a line is ready: Editor.Line
	Interrupt               // Ctrl-C on an empty line
	EOF                     // Ctrl-D on an empty line
	ClearScr                // Ctrl-L
)

// Editor is a one-line editor with history and the slash-command menu. It is pure: it takes keys
// and changes its state; the Console draws it.
type Editor struct {
	Buf       []rune
	Pos       int
	Sel       int  // selected menu item
	Dismissed bool // Esc closed the menu until the text changes
	Line      string

	history []string
	hist    int // index into history while browsing; len(history) when not
	draft   string
}

// NewEditor returns an empty editor.
func NewEditor() *Editor { return &Editor{} }

// Text is what is typed.
func (e *Editor) Text() string { return string(e.Buf) }

// Menu returns the commands to offer under the input line: only while the text starts with "/",
// has no space yet, and the menu wasn't dismissed.
func (e *Editor) Menu() []Command {
	if e.Dismissed {
		return nil
	}
	return Filter(string(e.Buf))
}

func (e *Editor) changed() { e.Sel, e.Dismissed = 0, false }

func (e *Editor) set(s string) {
	e.Buf = []rune(s)
	e.Pos = len(e.Buf)
	e.changed()
}

// Reset clears the input (after a submit).
func (e *Editor) Reset() {
	e.Buf, e.Pos, e.draft = nil, 0, ""
	e.hist = len(e.history)
	e.changed()
}

// Handle applies one key.
func (e *Editor) Handle(k Key) Result {
	menu := e.Menu()
	switch k.Kind {
	case KeyRune:
		e.Buf = append(e.Buf[:e.Pos], append([]rune{k.Rune}, e.Buf[e.Pos:]...)...)
		e.Pos++
		e.changed()
	case KeyBackspace:
		if e.Pos > 0 {
			e.Buf = append(e.Buf[:e.Pos-1], e.Buf[e.Pos:]...)
			e.Pos--
			e.changed()
		}
	case KeyDelete:
		if e.Pos < len(e.Buf) {
			e.Buf = append(e.Buf[:e.Pos], e.Buf[e.Pos+1:]...)
			e.changed()
		}
	case KeyLeft:
		e.Pos = max(0, e.Pos-1)
	case KeyRight:
		e.Pos = min(len(e.Buf), e.Pos+1)
	case KeyHome:
		e.Pos = 0
	case KeyEnd:
		e.Pos = len(e.Buf)
	case KeyCtrlU:
		e.Buf, e.Pos = e.Buf[e.Pos:], 0
		e.changed()
	case KeyCtrlK:
		e.Buf = e.Buf[:e.Pos]
		e.changed()
	case KeyCtrlW:
		i := e.Pos
		for i > 0 && e.Buf[i-1] == ' ' {
			i--
		}
		for i > 0 && e.Buf[i-1] != ' ' {
			i--
		}
		e.Buf = append(e.Buf[:i], e.Buf[e.Pos:]...)
		e.Pos = i
		e.changed()
	case KeyCtrlL:
		return ClearScr
	case KeyEsc:
		e.Dismissed = true
	case KeyUp:
		if len(menu) > 0 {
			e.Sel = (e.Sel - 1 + len(menu)) % len(menu)
		} else {
			e.browse(-1)
		}
	case KeyDown:
		if len(menu) > 0 {
			e.Sel = (e.Sel + 1) % len(menu)
		} else {
			e.browse(1)
		}
	case KeyTab:
		if len(menu) > 0 {
			e.set(Complete(menu[e.Sel%len(menu)]))
		}
	case KeyEnter:
		if len(menu) > 0 {
			// Run the highlighted command; a space already typed means arguments follow, and
			// then the menu is closed, so this only runs names.
			e.Line = "/" + menu[e.Sel%len(menu)].Name
		} else {
			e.Line = string(e.Buf)
		}
		if strings.TrimSpace(e.Line) != "" && (len(e.history) == 0 || e.history[len(e.history)-1] != e.Line) {
			e.history = append(e.history, e.Line)
		}
		e.Reset()
		return Submit
	case KeyCtrlC:
		if len(e.Buf) > 0 {
			e.Reset()
			return None
		}
		return Interrupt
	case KeyCtrlD:
		if len(e.Buf) == 0 {
			return EOF
		}
		if e.Pos < len(e.Buf) {
			e.Buf = append(e.Buf[:e.Pos], e.Buf[e.Pos+1:]...)
			e.changed()
		}
	}
	return None
}

// browse moves through the history: -1 older, +1 newer.
func (e *Editor) browse(dir int) {
	if len(e.history) == 0 {
		return
	}
	if e.hist == len(e.history) {
		e.draft = string(e.Buf)
	}
	e.hist = min(len(e.history), max(0, e.hist+dir))
	if e.hist == len(e.history) {
		e.set(e.draft)
		return
	}
	e.set(e.history[e.hist])
}
