package commands

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/termtest"
)

var rawID = regexp.MustCompile(`usr_|run_|prm_`)

// withoutLinks drops the lines that carry a session link or the "reopen with" hint: they are
// the one place a session id is meant to be seen, because it is what you type to come back.
func withoutLinks(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "http") && !strings.Contains(l, "reopen with") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// screen is what a person sees: the chat's raw output played onto a terminal.
func (c *chatRun) screen() *termtest.Term { return termtest.Play(80, c.out.String()) }

// waitScreen waits until check holds for the screen (the status line redraws all the time, so a
// half-drawn moment simply fails the check and the next one is looked at).
func (c *chatRun) waitScreen(t *testing.T, what string, check func(lines []string, term *termtest.Term) bool) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		term := c.screen()
		if lines := term.Lines(); check(lines, term) {
			return lines
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; screen:\n%s", what, strings.Join(c.screen().Lines(), "\n"))
	return nil
}

func count(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

func TestChatOwnerExampleShowsEachThingOnceAndNoIDs(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	answer := `I see "abcabc." If you're testing the chat, it's working—what can I help you with?`
	h.f.ws["ses_01"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "chat.message", pl{"messageId": "msg_1", "authorId": "usr_1", "text": "abcabc", "kind": "ask", "offRecord": false}),
		ev(2, "prompt.queued", pl{"promptId": "prm_1", "authorId": "usr_1", "text": "abcabc", "mode": "ask", "position": 0}),
		ev(3, "prompt.started", pl{"promptId": "prm_1", "runId": "run_1"}),
		ev(4, "agent.message.delta", pl{"runId": "run_1", "text": `I see "abcabc." `}),
		ev(5, "agent.message.delta", pl{"runId": "run_1", "text": "If you're testing the chat, it's working—what can I help you with?"}),
		ev(6, "agent.message", pl{"runId": "run_1", "text": answer}),
		ev(7, "run.completed", pl{"runId": "run_1", "outcome": "success", "summary": answer})}}}
	c := h.startChat(false, "chat", "--chat")
	c.send("abcabc\n")
	_ = c.in.Close()
	eq(t, c.finish(t), 0, "exit")
	out := c.out.String()
	eq(t, strings.Count(out, "› "), 1, "your message is shown once: as you typed it")
	contains(t, out, "› abcabc\n")
	eq(t, strings.Count(out, "abcabc"), 2, "the typed line and the quote in the answer")
	eq(t, strings.Count(out, "what can I help you with"), 1, "the answer is printed once")
	contains(t, out, "✓ done")
	notContains(t, out, "run completed")
	notContains(t, out, "run started")
	notContains(t, out, "[ask]")
	notContains(t, out, "ask (")
	notContains(t, out, "prompt (")
	if m := rawID.FindString(withoutLinks(out)); m != "" {
		t.Fatalf("an id is shown (%s):\n%s", m, out)
	}
}

func TestChatKeepsTypingAndQueuesWhileWaniisWorks(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya", "name": "Maya L"}}
	h.f.sessions = append(h.f.sessions, session("ses_a", "Checkout", "running", 0, "acme/api", 3))
	h.f.addEvents("ses_a",
		ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "usr_1", "text": "fix checkout", "mode": "build", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "p1", "runId": "r1"}))
	h.f.running["ses_a"] = true
	live := make(chan map[string]any, 32)
	h.f.ws["ses_a"] = []wsScript{{hold: true, live: live}}
	c := h.startChat(true, "chat", "ses_a")
	c.waitFor(t, "Checkout")

	// The person starts typing; Waniis streams while they do.
	c.send("also add a te")
	live <- ev(3, "agent.message.delta", pl{"runId": "r1", "text": "Looking at the retry "})
	lines := c.waitScreen(t, "the streamed text above the typed text", func(l []string, term *termtest.Term) bool {
		return len(l) >= 3 && strings.Contains(l[len(l)-3], "Waniis: Looking at the retry") &&
			strings.Contains(l[len(l)-2], "Writing the answer…") && l[len(l)-1] == "› also add a te" &&
			term.CursorLine() == "› also add a te" && term.C == 2+len("also add a te")
	})
	if !strings.ContainsAny(lines[len(lines)-2], "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") || !strings.Contains(lines[len(lines)-2], " · ") {
		t.Fatalf("the status line has a spinner and the elapsed time: %q", lines[len(lines)-2])
	}

	// A tool call changes the status; the buffer survives the redraw.
	live <- ev(4, "tool.call", pl{"runId": "r1", "toolCallId": "t1", "tool": "github.get_file", "args": pl{"repo": "acme/api", "path": "src/x.ts"}})
	c.waitScreen(t, "the status follows the tool call", func(l []string, term *termtest.Term) bool {
		return len(l) >= 2 && strings.Contains(l[len(l)-2], "Reading acme/api…") && l[len(l)-1] == "› also add a te" && term.C == 2+len("also add a te")
	})
	c.send("st") // keeps typing: "also add a test"

	// Enter while a run is going queues the message.
	c.send("\r")
	c.waitFor(t, "queued — Waniis will read it next")
	cmds := h.f.commandsOf("prompt.submit")
	eq(t, len(cmds), 1, "one prompt")
	eq(t, cmds[0]["text"], "also add a test", "text")
	c.waitScreen(t, "the sent message stays on screen and the input is empty", func(l []string, term *termtest.Term) bool {
		return count(l, "› also add a test") == 1 && l[len(l)-1] == "›" && term.CursorLine() == "›"
	})
	// What the server repeats of it is not shown again.
	live <- ev(5, "chat.message", pl{"messageId": "m2", "authorId": "usr_1", "text": "also add a test", "kind": "chat", "offRecord": false})
	live <- ev(6, "prompt.queued", pl{"promptId": "prm_1", "authorId": "usr_1", "text": "also add a test", "mode": "build", "position": 1})

	// /steer works at any time too, and so can another message.
	c.send("/steer slower\r")
	c.waitFor(t, "Steering sent.")
	eq(t, h.f.commandsOf("run.steer")[0]["text"], "slower", "steer")
	c.send("one more thing\r")
	for i := 0; i < 1000 && len(h.f.commandsOf("prompt.submit")) < 2; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	eq(t, len(h.f.commandsOf("prompt.submit")), 2, "a second prompt queued mid-run")
	eq(t, h.f.commandsOf("prompt.submit")[1]["text"], "one more thing", "its text")

	// The run ends and the queued message starts: the answer once, one quiet line.
	live <- ev(7, "agent.message.delta", pl{"runId": "r1", "text": "loop."})
	live <- ev(8, "agent.message", pl{"runId": "r1", "text": "Looking at the retry loop."})
	live <- ev(9, "run.completed", pl{"runId": "r1", "outcome": "success", "summary": "Looking at the retry loop."})
	lines = c.waitScreen(t, "the end of the first run", func(l []string, term *termtest.Term) bool {
		return count(l, "✓ done · 7s") == 1 && l[len(l)-1] == "›"
	})
	eq(t, count(lines, "Waniis:"), 1, "the answer has one label, though text was printed in between")
	eq(t, count(lines, "› also add a test"), 1, "your message once")
	eq(t, count(lines, "› one more thing"), 1, "your second message")
	if !strings.Contains(lines[len(lines)-2], "…") {
		t.Fatalf("the status goes on while a queued message is still to be read: %q", lines[len(lines)-2])
	}
	live <- ev(10, "prompt.started", pl{"promptId": "prm_1", "runId": "r2"})
	live <- ev(11, "agent.message", pl{"runId": "r2", "text": "Added the test."})
	live <- ev(12, "run.completed", pl{"runId": "r2", "outcome": "success", "summary": "Added the test."})
	lines = c.waitScreen(t, "the end of the second run", func(l []string, term *termtest.Term) bool {
		return count(l, "✓ done") == 2 && l[len(l)-1] == "›" && strings.HasPrefix(l[len(l)-2], "✓ done")
	})
	eq(t, count(lines, "Writing the answer"), 0, "the status line is gone")
	if m := rawID.FindString(strings.Join(lines, "\n")); m != "" {
		t.Fatalf("an id on screen: %s", strings.Join(lines, "\n"))
	}
	c.send("/quit\r")
	eq(t, c.finish(t), 0, "exit")
}

func TestChatRefreshesPeopleWhenSomeoneNewAppears(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya", "name": "Maya L"}}
	h.f.sessions = append(h.f.sessions, session("ses_a", "Checkout", "idle", 0, "acme/api", 3))
	live := make(chan map[string]any, 8)
	h.f.ws["ses_a"] = []wsScript{{hold: true, live: live}}
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Checkout")
	// Sara joins the session after the chat opened; her message names her once the people are read again.
	h.f.mu.Lock()
	h.f.people = append(h.f.people, map[string]any{"id": "usr_sara0000000000000000", "login": "sara", "name": "Sara Lee"})
	h.f.mu.Unlock()
	live <- ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "usr_sara0000000000000000", "text": "any luck?", "mode": "build", "position": 0})
	c.waitFor(t, "› Sara Lee: any luck?")
	// Someone the people route doesn't know reads "someone", never the id.
	live <- ev(2, "prompt.queued", pl{"promptId": "p2", "authorId": "usr_ghost000000000000000", "text": "hello?", "mode": "build", "position": 1})
	c.waitFor(t, "› someone: hello?")
	live <- ev(3, "run.steered", pl{"runId": "r", "authorId": "usr_1", "text": "mine"})
	live <- ev(4, "run.steered", pl{"runId": "r", "authorId": "usr_sara0000000000000000", "text": "hers"})
	c.waitFor(t, "↪ Sara Lee steered: hers")
	c.send("/quit\n")
	eq(t, c.finish(t), 0, "exit")
	out := c.out.String()
	notContains(t, out, "mine") // your own steer is already on screen
	if m := rawID.FindString(withoutLinks(out)); m != "" {
		t.Fatalf("an id is shown (%s):\n%s", m, out)
	}
}

func TestLogsNameYouOthersAndSomeoneNeverIDs(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya", "name": "Maya L"}, {"id": "usr_01sam00000000000000000sam", "login": "sam", "name": "usr_01sam00000000000000000sam"}}
	h.f.addEvents("ses_1",
		ev(1, "prompt.queued", pl{"promptId": "prm_1", "authorId": "usr_1", "text": "mine", "mode": "build", "position": 0}),
		ev(2, "chat.message", pl{"messageId": "msg_1", "authorId": "usr_01sam00000000000000000sam", "text": "his", "kind": "ask", "offRecord": false}),
		ev(3, "chat.message", pl{"messageId": "msg_2", "authorId": "usr_404", "text": "unknown", "kind": "chat", "offRecord": false}),
		ev(4, "prompt.started", pl{"promptId": "prm_1", "runId": "run_1"}),
		ev(5, "run.completed", pl{"runId": "run_1", "outcome": "success", "summary": "done"}))
	for _, args := range [][]string{{"logs", "ses_1"}, {"sessions", "logs", "ses_1", "--all"}} {
		code, out, _ := h.run(args...)
		eq(t, code, 0, "exit")
		contains(t, out, "› you: mine")
		contains(t, out, "› sam: his")
		contains(t, out, "› someone: unknown")
		if m := rawID.FindString(out); m != "" {
			t.Fatalf("an id is shown (%s):\n%s", m, out)
		}
	}
	// --json keeps every id: it is the raw events.
	_, out, _ := h.run("logs", "ses_1", "--json")
	contains(t, out, `"authorId":"usr_1"`)
	contains(t, out, `"runId":"run_1"`)
}

func TestStatusShowsNoRunID(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.sessions = append(h.f.sessions, session("ses_1", "t", "running", 0, "acme/api", 1))
	h.f.status["ses_1"] = pl{"stage": "working", "activity": "running", "run": pl{"id": "run_9", "promptId": "prm_9", "mode": "build", "stage": "working"}, "queued": 0, "lastRun": nil, "pullRequest": nil, "sandbox": nil, "lastSeq": 3}
	_, out, _ := h.run("sessions", "status", "ses_1")
	notContains(t, out, "run_9")
	notContains(t, out, "prm_9")
	_, out, _ = h.run("sessions", "status", "ses_1", "--json")
	contains(t, out, "run_9")
}
