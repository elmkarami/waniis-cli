package chat

import (
	"strings"
	"testing"
)

func names(cs []Command) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Name)
	}
	return strings.Join(n, ",")
}

func TestFilter(t *testing.T) {
	if got := Filter("/"); len(got) != len(Commands) {
		t.Fatalf("slash alone lists everything: %d", len(got))
	}
	if got := names(Filter("/st")); got != "status,stop" && !strings.HasPrefix(got, "status,stop") {
		t.Fatalf("prefix matches first: %s", got)
	}
	if got := names(Filter("/ST")); !strings.HasPrefix(got, "status,stop") {
		t.Fatalf("case-insensitive: %s", got)
	}
	if got := names(Filter("/exit")); got != "quit" {
		t.Fatalf("alias: %s", got)
	}
	if got := names(Filter("/clip")); got != "" {
		t.Fatalf("no match: %q", got)
	}
	if Filter("hello") != nil || Filter("/steer now") != nil || Filter("") != nil {
		t.Fatal("only a bare slash word filters")
	}
	// A description match comes after the name matches.
	got := names(Filter("/browser"))
	if got != "open" {
		t.Fatalf("description match: %q", got)
	}
}

func TestLookupAndComplete(t *testing.T) {
	c, ok := Lookup("EXIT")
	if !ok || c.Name != "quit" {
		t.Fatal("alias lookup")
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown")
	}
	steer, _ := Lookup("steer")
	if Complete(steer) != "/steer " {
		t.Fatalf("%q", Complete(steer))
	}
	stop, _ := Lookup("stop")
	if Complete(stop) != "/stop" {
		t.Fatalf("%q", Complete(stop))
	}
	if len(HelpLines()) != len(Commands) {
		t.Fatal("help lists all")
	}
}

func typeKeys(e *Editor, s string) {
	for _, k := range Decode([]byte(s)) {
		e.Handle(k)
	}
}

func TestDecode(t *testing.T) {
	cases := map[string][]KeyKind{
		"a":              {KeyRune},
		"\x1b":           {KeyEsc},
		"\x1b[A\x1b[B":   {KeyUp, KeyDown},
		"\x1b[C\x1b[D":   {KeyRight, KeyLeft},
		"\x1b[3~":        {KeyDelete},
		"\x1b[H\x1b[F":   {KeyHome, KeyEnd},
		"\r\n":           {KeyEnter},
		"\x7f\t\x03\x04": {KeyBackspace, KeyTab, KeyCtrlC, KeyCtrlD},
		"é":              {KeyRune},
		"\x1bOA":         {KeyUp},
	}
	for in, want := range cases {
		keys := Decode([]byte(in))
		if len(keys) != len(want) {
			t.Fatalf("%q: %v", in, keys)
		}
		for i, k := range keys {
			if k.Kind != want[i] {
				t.Fatalf("%q[%d]: %v", in, i, k)
			}
		}
	}
}

func TestEditorTypingEditing(t *testing.T) {
	e := NewEditor()
	typeKeys(e, "helo")
	typeKeys(e, "\x1b[D")
	typeKeys(e, "l")
	if e.Text() != "hello" || e.Pos != 4 {
		t.Fatalf("%q %d", e.Text(), e.Pos)
	}
	typeKeys(e, "\x7f")
	typeKeys(e, "\x1b[3~")
	if e.Text() != "hel" {
		t.Fatalf("%q", e.Text())
	}
	e.Handle(Key{Kind: KeyCtrlU})
	if e.Text() != "" {
		t.Fatal("ctrl-u")
	}
	typeKeys(e, "one two")
	e.Handle(Key{Kind: KeyCtrlW})
	if e.Text() != "one " {
		t.Fatalf("%q", e.Text())
	}
}

func TestMenuNavigationCompletionAndRun(t *testing.T) {
	e := NewEditor()
	typeKeys(e, "/")
	if len(e.Menu()) != len(Commands) {
		t.Fatal("menu on slash")
	}
	typeKeys(e, "st")
	if got := names(e.Menu()); !strings.HasPrefix(got, "status,stop") {
		t.Fatalf("filters as you type: %s", got)
	}
	typeKeys(e, "\x1b[B") // down: stop
	if e.Menu()[e.Sel].Name != "stop" {
		t.Fatalf("selected %v", e.Menu()[e.Sel])
	}
	typeKeys(e, "\x1b[B\x1b[B\x1b[B\x1b[B") // wraps
	typeKeys(e, "\t")
	if !strings.HasPrefix(e.Text(), "/") || len(e.Text()) < 4 {
		t.Fatalf("tab completes: %q", e.Text())
	}

	// Tab on a command with arguments leaves a space and closes the menu.
	e = NewEditor()
	typeKeys(e, "/ste\t")
	if e.Text() != "/steer " || len(e.Menu()) != 0 {
		t.Fatalf("%q", e.Text())
	}
	typeKeys(e, "make it faster")
	if r := e.Handle(Key{Kind: KeyEnter}); r != Submit || e.Line != "/steer make it faster" {
		t.Fatalf("%v %q", r, e.Line)
	}

	// Enter on the highlighted command runs it.
	e = NewEditor()
	typeKeys(e, "/sta")
	if r := e.Handle(Key{Kind: KeyEnter}); r != Submit || e.Line != "/status" {
		t.Fatalf("%v %q", r, e.Line)
	}

	// Esc dismisses until the text changes.
	e = NewEditor()
	typeKeys(e, "/q\x1b")
	if len(e.Menu()) != 0 {
		t.Fatal("dismissed")
	}
	typeKeys(e, "u")
	if len(e.Menu()) == 0 {
		t.Fatal("menu returns after a change")
	}
}

func TestEditorHistoryAndInterrupts(t *testing.T) {
	e := NewEditor()
	typeKeys(e, "first\r")
	typeKeys(e, "second\r")
	typeKeys(e, "dra")
	typeKeys(e, "\x1b[A")
	if e.Text() != "second" {
		t.Fatalf("%q", e.Text())
	}
	typeKeys(e, "\x1b[A")
	if e.Text() != "first" {
		t.Fatalf("%q", e.Text())
	}
	typeKeys(e, "\x1b[B\x1b[B")
	if e.Text() != "dra" {
		t.Fatalf("draft comes back: %q", e.Text())
	}
	if r := e.Handle(Key{Kind: KeyCtrlC}); r != None || e.Text() != "" {
		t.Fatal("ctrl-c clears the text first")
	}
	if r := e.Handle(Key{Kind: KeyCtrlC}); r != Interrupt {
		t.Fatal("then interrupts")
	}
	if r := e.Handle(Key{Kind: KeyCtrlD}); r != EOF {
		t.Fatal("ctrl-d on empty")
	}
	typeKeys(e, "x")
	if r := e.Handle(Key{Kind: KeyCtrlD}); r != None {
		t.Fatal("ctrl-d with text doesn't quit")
	}
}

func newConsole(raw bool) (*Console, *strings.Builder) {
	var out strings.Builder
	ed := NewEditor()
	return &Console{Out: &out, Raw: raw, Width: func() int { return 60 }, Prompt: func() string { return "› " }, Ed: ed}, &out
}

func TestConsolePlainPassesThrough(t *testing.T) {
	c, out := newConsole(false)
	c.Write([]byte("hello\nworld\n"))
	c.Redraw()
	if out.String() != "hello\nworld\n" {
		t.Fatalf("%q", out.String())
	}
}

func TestConsoleRawKeepsTheInputLine(t *testing.T) {
	c, out := newConsole(true)
	typeKeys(c.Ed, "/st")
	c.Write([]byte("an event\n"))
	s := out.String()
	if !strings.Contains(s, "an event\x1b[0m\r\n") {
		t.Fatalf("output goes above the prompt: %q", s)
	}
	tail := s[strings.Index(s, "an event"):]
	if !strings.Contains(tail, "› /st") || !strings.Contains(tail, "/status") || !strings.Contains(tail, "/stop") {
		t.Fatalf("prompt and menu are drawn again: %q", tail)
	}
	if !strings.HasSuffix(tail, "\r\x1b[5C") {
		t.Fatalf("cursor goes back to the input line: %q", tail)
	}

	// A half-written line (streamed text) is shown above the input line, which stays drawn.
	out.Reset()
	c.Write([]byte("Waniis: Hel"))
	s = out.String()
	if !strings.Contains(s, "Waniis: Hel\x1b[0m\r\n› /st") {
		t.Fatalf("the partial line sits above the prompt: %q", s)
	}
	out.Reset()
	c.Write([]byte("lo\n"))
	s = out.String()
	if strings.Count(s, "› /st\r\n") != 1 || !strings.Contains(s, "Waniis: Hello\x1b[0m\r\n") {
		t.Fatalf("%q", s)
	}
}

func TestConsoleMenuScrollsAndLongInputIsWindowed(t *testing.T) {
	c, _ := newConsole(true)
	typeKeys(c.Ed, "/")
	for i := 0; i < 10; i++ {
		c.Ed.Handle(Key{Kind: KeyDown})
	}
	lines := c.menuLines(60)
	if len(lines) != MaxMenu {
		t.Fatalf("%d menu lines", len(lines))
	}
	selected := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "›") {
			selected++
		}
	}
	if selected != 1 {
		t.Fatal("exactly one selected line")
	}
	c.Ed.Reset()
	typeKeys(c.Ed, strings.Repeat("x", 200))
	d := c.draw()
	if strings.Count(d, "x") > 60 {
		t.Fatalf("input is windowed to the width: %d", strings.Count(d, "x"))
	}
}
