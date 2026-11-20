package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/mark"
	"github.com/elmkarami/waniis-cli/internal/style"
	"github.com/elmkarami/waniis-cli/internal/termtest"
)

// screenConsole is a raw console drawing onto a fake terminal of the given width.
func screenConsole(width int) (*Console, *termtest.Term) {
	term := termtest.New(width)
	ed := NewEditor()
	return &Console{Out: term, Raw: true, Width: func() int { return width }, Prompt: func() string { return "› " }, Ed: ed}, term
}

func eqLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("screen:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestStreamingTextShowsAboveTheInputAndTheTypedTextSurvives(t *testing.T) {
	c, term := screenConsole(40)
	c.Redraw()
	typeKeys(c.Ed, "next idea")
	c.Redraw()
	// Waniis streams a few words at a time, with no newline for a while.
	for _, chunk := range []string{"Waniis: ", "Looking ", "at the ", "retry loop"} {
		c.Write([]byte(chunk))
		c.Redraw()
	}
	eqLines(t, term.Lines(), "Waniis: Looking at the retry loop", "› next idea")
	if term.CursorLine() != "› next idea" || term.C != 2+len("next idea") {
		t.Fatalf("the cursor stays at the end of what was typed: row %q col %d", term.CursorLine(), term.C)
	}
	c.Write([]byte("\n⚙ tool read src/x.ts\n"))
	eqLines(t, term.Lines(), "Waniis: Looking at the retry loop", "⚙ tool read src/x.ts", "› next idea")
	// Typing goes on while the answer streams.
	typeKeys(c.Ed, " and more")
	c.Write([]byte("Waniis: done"))
	eqLines(t, term.Lines(), "Waniis: Looking at the retry loop", "⚙ tool read src/x.ts", "Waniis: done", "› next idea and more")
	if term.C != 2+len("next idea and more") {
		t.Fatalf("cursor column %d", term.C)
	}
}

func TestLongStreamedLinesWrapAtWordsWithoutLeavingStrayRows(t *testing.T) {
	c, term := screenConsole(30)
	c.Redraw()
	words := strings.Fields("Waniis: The checkout test is flaky because the retry loop reads the clock twice and the fixture never resets it")
	for _, w := range words {
		c.Write([]byte(w + " "))
	}
	c.Write([]byte("\n"))
	lines := term.Lines()
	if last := lines[len(lines)-1]; last != "›" {
		t.Fatalf("the prompt is the last row, got %q in\n%s", last, strings.Join(lines, "\n"))
	}
	for i, l := range lines {
		if Width(l) > 29 {
			t.Errorf("row %d is %d columns: %q", i, Width(l), l)
		}
		if i > 0 && i < len(lines)-1 && !strings.HasPrefix(l, "  ") {
			t.Errorf("continuation rows are indented: %q", l)
		}
	}
	if strings.Join(strings.Fields(strings.Join(lines[:len(lines)-1], " ")), " ") != strings.Join(words, " ") {
		t.Fatalf("no words lost: %v", lines)
	}
}

func TestStatusLineSitsAboveTheInputAndGoesAway(t *testing.T) {
	c, term := screenConsole(50)
	c.Redraw()
	typeKeys(c.Ed, "hi")
	c.SetStatus("⠋ Reading acme/api… · 2s")
	eqLines(t, term.Lines(), "⠋ Reading acme/api… · 2s", "› hi")
	c.Write([]byte("● session started\n"))
	eqLines(t, term.Lines(), "● session started", "⠋ Reading acme/api… · 2s", "› hi")
	c.SetStatus("⠙ Reading acme/api… · 2s")
	c.Write([]byte("Waniis: part"))
	eqLines(t, term.Lines(), "● session started", "Waniis: part", "⠙ Reading acme/api… · 2s", "› hi")
	c.SetStatus("")
	eqLines(t, term.Lines(), "● session started", "Waniis: part", "› hi")
	c.Hide()
	eqLines(t, term.Lines(), "● session started", "Waniis: part")
}

func TestStatusLineAndMenuTogether(t *testing.T) {
	c, term := screenConsole(60)
	c.SetStatus("• Thinking… · 1s")
	typeKeys(c.Ed, "/sta")
	c.Redraw()
	lines := term.Lines()
	if lines[0] != "• Thinking… · 1s" || lines[1] != "› /sta" || !strings.Contains(lines[2], "/status") {
		t.Fatalf("%v", lines)
	}
	if term.R != 1 {
		t.Fatalf("the cursor is on the input row, not %d", term.R)
	}
	c.Write([]byte("event\n"))
	lines = term.Lines()
	if lines[0] != "event" || lines[1] != "• Thinking… · 1s" || lines[2] != "› /sta" {
		t.Fatalf("%v", lines)
	}
}

func TestOverlongStatusIsCutToOneRow(t *testing.T) {
	c, term := screenConsole(30)
	c.SetStatus("⠋ " + strings.Repeat("very long status ", 5))
	lines := term.Lines()
	if len(lines) != 2 || Width(lines[0]) > 29 || !strings.HasSuffix(lines[0], "…") {
		t.Fatalf("%v", lines)
	}
}

func TestPlainConsoleIgnoresTheStatusLine(t *testing.T) {
	c, out := newConsole(false)
	c.SetStatus("⠋ Thinking")
	c.Write([]byte("a\n"))
	if out.String() != "a\n" {
		t.Fatalf("no cursor tricks without a terminal: %q", out.String())
	}
}

// ---- the status line's words ----

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)} }
func event(typ string, payload any) api.Event {
	b, _ := json.Marshal(payload)
	return api.Event{Type: typ, Payload: b}
}

type m = map[string]any

func TestDescribeFollowsWhatIsReallyHappening(t *testing.T) {
	for _, c := range []struct {
		ev   api.Event
		want string
	}{
		{event("tool.call", m{"tool": "github.get_file", "args": m{"repo": "acme/api", "path": "src/x.ts"}}), "Reading acme/api…"},
		{event("tool.call", m{"tool": "read", "args": m{"filePath": "src/checkout.ts"}}), "Reading src/checkout.ts…"},
		{event("tool.call", m{"tool": "sentry.search_issues", "args": m{"query": "checkout"}}), "Searching Sentry…"},
		{event("tool.call", m{"tool": "sentry_search_issues", "args": m{}}), "Searching Sentry…"},
		{event("tool.call", m{"tool": "bash", "args": m{"command": "pnpm test --filter api"}}), "Running tests…"},
		{event("tool.call", m{"tool": "bash", "args": m{"command": "go test ./..."}}), "Running tests…"},
		{event("tool.call", m{"tool": "bash", "args": m{"command": "git push origin fix"}}), "Pushing…"},
		{event("tool.call", m{"tool": "bash", "args": m{"command": "ls -la src"}}), "Running ls -la src…"},
		{event("tool.call", m{"tool": "write", "args": m{"path": "src/routes/checkout.ts"}}), "Writing src/routes/checkout.ts…"},
		{event("tool.call", m{"tool": "edit", "args": m{"file_path": "a.go"}}), "Writing a.go…"},
		{event("tool.call", m{"tool": "grep", "args": m{"pattern": "retry"}}), "Searching the code…"},
		{event("tool.call", m{"tool": "webfetch", "args": m{"url": "https://docs.example.com/x"}}), "Fetching docs.example.com…"},
		{event("tool.call", m{"tool": "github.create_pull_request", "args": m{}}), "Opening a pull request…"},
		{event("tool.call", m{"tool": "linear.create_issue", "args": m{}}), "Updating Linear…"},
		{event("tool.call", m{"tool": "rafiiq-browser_http_request", "args": m{}}), "Asking Waniis browser…"},
		{event("file.changed", m{"path": "src/a.ts", "changeType": "modified"}), "Writing src/a.ts…"},
		{event("verification.started", m{}), "Running checks…"},
		{event("review.started", m{}), "Reviewing the change…"},
		{event("changes.publish_requested", m{}), "Opening a pull request…"},
		{event("sandbox.status", m{"status": "provisioning"}), "Starting a sandbox…"},
		{event("network.access_requested", m{}), "Waiting for your answer…"},
		{event("agent.message.delta", m{"text": "x"}), "Writing the answer…"},
		{event("pr.opened", m{}), ""},
	} {
		if got := Describe(c.ev); got != c.want {
			t.Errorf("%s %s: got %q want %q", c.ev.Type, c.ev.Payload, got, c.want)
		}
	}
}

func TestStatusTextRealUpdatesThenWordsThenElapsed(t *testing.T) {
	clk := newClock()
	a := NewActivity(clk.now)
	if a.Text() != "" || a.Line(style.Style{}, 0, true) != "" {
		t.Fatal("nothing to say while idle")
	}
	a.Start()
	if got := a.Text(); !strings.HasSuffix(got, "· 0s") || !strings.Contains(got, "…") {
		t.Fatalf("starts with one of the words: %q", got)
	}
	clk.add(2 * time.Second)
	a.Observe(event("tool.call", m{"tool": "github.get_file", "args": m{"repo": "acme/api"}}))
	clk.add(time.Second)
	if got := a.Text(); got != "Reading acme/api… · 3s" {
		t.Fatalf("a real update shows at once: %q", got)
	}
	clk.add(10 * time.Second)
	got := a.Text()
	if strings.Contains(got, "Reading") || !strings.HasSuffix(got, "· 13s") {
		t.Fatalf("between real updates the words come back: %q", got)
	}
	// The words rotate every few seconds, deterministically.
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		clk.add(3 * time.Second)
		seen[strings.SplitN(a.Text(), " · ", 2)[0]] = true
	}
	if len(seen) < 10 {
		t.Fatalf("the words rotate: %v", seen)
	}
	clk.add(2 * time.Minute)
	if !strings.HasSuffix(a.Text(), "· 2m 49s") {
		t.Fatalf("minutes: %q", a.Text())
	}
	a.Stop()
	if a.Text() != "" || a.Active() {
		t.Fatal("it disappears when the run ends")
	}
}

func TestStatusLineSpinnerAndStaticDot(t *testing.T) {
	clk := newClock()
	a := NewActivity(clk.now)
	a.Start()
	if l := a.Line(style.Style{}, 3, true); !strings.HasPrefix(l, SpinnerFrames[3]+" ") {
		t.Fatalf("%q", l)
	}
	if l := a.Line(style.Style{}, 3, false); !strings.HasPrefix(l, "• ") {
		t.Fatalf("no spinner: a static dot: %q", l)
	}
	if l := a.Line(style.Style{On: true}, 0, true); !strings.Contains(l, "\x1b[") {
		t.Fatal("colored on a terminal that allows it")
	}
}

func TestWordsAreWaniisOwn(t *testing.T) {
	if len(Words) < 12 {
		t.Fatalf("%d words", len(Words))
	}
	seen := map[string]bool{}
	for _, w := range Words {
		if seen[w] || strings.TrimSpace(w) == "" {
			t.Fatalf("duplicate or empty word %q", w)
		}
		seen[w] = true
	}
	// Words Claude Code uses on its own status line must not be copied.
	for _, w := range []string{"Pondering", "Cogitating", "Ruminating", "Percolating", "Noodling", "Simmering", "Brewing", "Musing", "Churning", "Spelunking"} {
		if seen[w] {
			t.Fatalf("%q is not ours", w)
		}
	}
}

func TestActivityBlockDrawsTheMark(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	a := NewActivity(func() time.Time { return now })
	sp := mark.NewSpinner(mark.Color256)
	if a.Block(style.Style{}, 0, sp) != "" {
		t.Fatal("nothing while idle")
	}
	a.Start()
	now = now.Add(5 * time.Second)
	lines := strings.Split(a.Block(style.Style{}, 0, sp), "\n")
	if len(lines) != 2 {
		t.Fatalf("two lines: %q", lines)
	}
	m := sp.Lines(0)
	if !strings.HasPrefix(lines[0], m[0]) || !strings.HasPrefix(lines[1], m[1]) {
		t.Fatalf("the mark spans both lines: %q", lines)
	}
	if !strings.HasSuffix(lines[0], "… · 5s") || !strings.HasSuffix(lines[1], stopHint) {
		t.Fatalf("%q", lines)
	}
	if a.Block(style.Style{}, 3, sp) == a.Block(style.Style{}, 0, sp) {
		t.Fatal("the mark turns from frame to frame")
	}
}
