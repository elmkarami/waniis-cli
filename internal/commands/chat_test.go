package commands

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type chatRun struct {
	out, err *syncBuf
	in       *io.PipeWriter
	done     chan int
}

// startChat runs the CLI with a controllable stdin. tty makes it a terminal in raw mode.
func (h *harness) startChat(tty bool, args ...string) *chatRun {
	h.t.Helper()
	pr, pw := io.Pipe()
	out, errb := &syncBuf{}, &syncBuf{}
	d := Deps{In: pr, Out: out, Err: errb,
		Getenv: func(k string) string { return h.env[k] }, HTTP: h.f.srv.Client(), Store: h.store,
		OpenBrowser: func(u string) error { h.opened = append(h.opened, u); return nil },
		Sleep:       func(ctx context.Context, d time.Duration) error { return ctx.Err() },
		Now:         func() time.Time { return h.now }, NewKey: h.newKey,
		StdinTTY: tty, StdoutTTY: tty, GitOrigin: func() string { return h.git },
		Hostname: func() string { return "h" }, PollInterval: time.Millisecond, RetryDelay: time.Millisecond,
		TermWidth: func() int { return 80 }}
	if tty {
		d.MakeRaw = func() (func(), error) { return func() {}, nil }
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(func() { cancel(); _ = pw.Close() })
	run := &chatRun{out: out, err: errb, in: pw, done: make(chan int, 1)}
	go func() { run.done <- Execute(ctx, d, append([]string{"--api-url", h.f.srv.URL}, args...)) }()
	return run
}

func (c *chatRun) send(s string) { _, _ = io.WriteString(c.in, s) }

func (c *chatRun) waitFor(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(c.out.String(), sub) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in:\n%s", sub, c.out.String())
}

func (c *chatRun) finish(t *testing.T) int {
	t.Helper()
	select {
	case code := <-c.done:
		return code
	case <-time.After(5 * time.Second):
		t.Fatalf("chat didn't exit; output:\n%s", c.out.String())
		return -1
	}
}

func TestChatPipedPromptStreamsTheRunThenLeaves(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.ws["ses_01"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "usr_1", "text": "why is CI red?", "mode": "ask", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "p1", "runId": "r1"}),
		ev(3, "agent.message", pl{"runId": "r1", "text": "The lockfile drifted."}),
		runDone(4, "success")}}}
	c := h.startChat(false, "chat", "--chat")
	c.send("why is CI red?\n")
	_ = c.in.Close()
	eq(t, c.finish(t), 0, "exit")
	out := c.out.String()
	contains(t, out, "Started session ses_01")
	contains(t, out, "Waniis: The lockfile drifted.")
	contains(t, out, "■ run completed")
	contains(t, out, "reopen with `waniis chat ses_01`")
	eq(t, h.f.last("POST", "/v1/sessions").Body["stage"], "chat", "no repo")
	notContains(t, out, "\x1b[")
	noSecrets(t, out, c.err.String())
}

func TestChatStartsFromTheCheckoutRepo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.git = "git@github.com:acme/api.git"
	h.f.ws["ses_01"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "u", "text": "x", "mode": "build", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "p1", "runId": "r1"}), runDone(3, "success")}}}
	c := h.startChat(false, "chat")
	c.send("fix it\n")
	_ = c.in.Close()
	eq(t, c.finish(t), 0, "exit")
	repos := h.f.last("POST", "/v1/sessions").Body["repos"].([]any)
	eq(t, repos[0].(map[string]any)["cloneUrl"].(string), "https://github.com/acme/api.git", "repo from git origin")
}

func TestChatFallsBackToAConversationWithoutARepo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.ws["ses_01"] = []wsScript{{hold: true, events: []map[string]any{runDone(1, "success")}}}
	c := h.startChat(false, "chat")
	c.send("hello\n")
	_ = c.in.Close()
	eq(t, c.finish(t), 0, "exit")
	contains(t, c.out.String(), "No git repo here: starting a conversation")
	eq(t, h.f.last("POST", "/v1/sessions").Body["stage"], "chat", "stage")
}

func seedChat(h *harness) {
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya", "name": "Maya L"}}
	h.f.sessions = append(h.f.sessions, session("ses_a", "Fix checkout", "idle", 5, "acme/api", 3), session("ses_b", "Other", "running", 0, "acme/api", 9))
	seedRun(h, "ses_a")
	h.f.addEvents("ses_a", runDone(5, "success"), ev(6, "pr.opened", pl{"provider": "github", "url": "https://github.com/acme/api/pull/5", "number": 5, "repo": "acme/api"}))
}

func TestChatAttachShowsRecentEventsAndSlashCommands(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	h.f.status["ses_a"] = pl{"stage": "working", "activity": "idle", "run": nil, "queued": 0, "lastRun": pl{"id": "r1", "outcome": "success", "summary": "did the thing"}, "pullRequest": nil, "sandbox": nil, "lastSeq": 6}
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Session ses_a")
	c.send("/help\n")
	c.waitFor(t, "/steer <text>")
	c.send("/status\n")
	c.waitFor(t, "Activity:")
	c.send("/logs 2\n")
	c.send("/pr\n")
	c.waitFor(t, "PR #5 https://github.com/acme/api/pull/5")
	c.send("/open\n")
	c.send("/share collaborator\n")
	c.waitFor(t, "Share link (collaborator)")
	c.send("/whoami\n")
	c.waitFor(t, "Maya L (maya)")
	c.send("/workspace\n")
	c.waitFor(t, "ws_1")
	c.send("/attach x.png\n")
	c.waitFor(t, "Not available yet")
	c.send("/queue\n")
	c.waitFor(t, "Nothing is queued")
	c.send("/nope\n")
	c.waitFor(t, "Unknown command /nope")
	c.send("/preview\n")
	c.waitFor(t, "Port 3000")
	c.send("/diff\n")
	c.waitFor(t, "sandbox asleep")
	c.send("/quit\n")
	eq(t, c.finish(t), 0, "exit")
	out := c.out.String()
	contains(t, out, "› prompt (maya): fix the flaky test") // recent events, with names
	contains(t, out, "https://p3000-abc.waniis.app/__handshake?ticket=T1")
	contains(t, out, "expire in about a minute")
	contains(t, out, "acme/api (pushed): 2 files, +13 −1 · PR #5")
	contains(t, out, "modified  src/x.ts (+3 −1)")
	contains(t, out, "reopen with `waniis chat ses_a`")
	if len(h.opened) < 2 {
		t.Fatalf("expected /pr and /open to open the browser, got %v", h.opened)
	}
	noSecrets(t, out, c.err.String())
}

func TestChatPromptModeModelAndQueueNotice(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.addEvents("ses_a", ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "u", "text": "x", "mode": "build", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "p1", "runId": "r1"}))
	h.f.sessions = append(h.f.sessions, session("ses_a", "t", "running", 0, "acme/api", 1))
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Session ses_a")
	c.send("/mode plan\n")
	c.send("/model claude-x\n")
	c.send("/mode bogus\n")
	c.waitFor(t, "Usage: /mode")
	c.send("also add a test\n")
	c.waitFor(t, "queued behind it")
	cmds := h.f.commandsOf("prompt.submit")
	eq(t, len(cmds), 1, "one prompt")
	eq(t, cmds[0]["mode"], "plan", "mode")
	eq(t, cmds[0]["model"], "claude-x", "model")
	eq(t, cmds[0]["text"], "also add a test", "text")
	c.send("/steer go slower\n")
	c.waitFor(t, "Steering sent")
	eq(t, h.f.commandsOf("run.steer")[0]["text"], "go slower", "steer")
	c.send("/stop\n")
	c.waitFor(t, "Stop requested")
	c.send("/quit\n")
	eq(t, c.finish(t), 0, "exit")
}

func TestChatSteerWhenNothingRuns(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Session ses_a")
	c.send("/steer faster\n")
	c.waitFor(t, "Nothing is running to steer")
	eq(t, len(h.f.commandsOf("run.steer")), 0, "nothing sent")
	c.send("/quit\n")
	c.finish(t)
}

func TestChatAnswersANetworkRequestInline(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.addEvents("ses_a", ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "u", "text": "x", "mode": "build", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "p1", "runId": "r1"}))
	h.f.sessions = append(h.f.sessions, session("ses_a", "t", "running", 0, "acme/api", 1))
	h.f.ws["ses_a"] = []wsScript{{hold: true, events: []map[string]any{
		ev(3, "network.access_requested", pl{"requestId": "n1", "host": "ramp.com", "access": "read", "reason": "docs", "runId": "r1", "expiresAt": "2026-11-20T13:00:00Z"})}}}
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Allow ramp.com (read) for this session? [y/N]")
	c.send("y\n")
	c.waitFor(t, "Allowed ramp.com for this session")
	body := h.f.last("POST", "/v1/sessions/ses_a/network/access").Body
	eq(t, body["kind"], "decide", "kind")
	eq(t, body["requestId"], "n1", "request")
	eq(t, body["decision"], "approve", "decision")
	eq(t, body["scope"], "session", "scope")
	c.send("/quit\n")
	c.finish(t)
}

func TestChatToolApprovalAndDeny(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.sessions = append(h.f.sessions, session("ses_a", "t", "running", 0, "acme/api", 1))
	h.f.ws["ses_a"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "mcp.approval_requested", pl{"requestId": "a1", "server": "github", "tool": "merge_pr", "risk": "write", "args": pl{}, "expiresAt": "2026-11-20T13:00:00Z"}),
		ev(2, "mcp.approval_requested", pl{"requestId": "a2", "server": "linear", "tool": "delete_issue", "risk": "destructive", "args": pl{}, "expiresAt": "2026-11-20T13:00:00Z"})}}}
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Allow github.merge_pr")
	c.send("s\n")
	c.waitFor(t, "Allow linear.delete_issue") // the next question comes after the first is answered
	c.send("\n")
	c.waitFor(t, "Denied.")
	cmds := h.f.commandsOf("tool.approval.decide")
	eq(t, len(cmds), 2, "two decisions")
	eq(t, cmds[0]["decision"], "session", "first")
	eq(t, cmds[1]["decision"], "deny", "default is no")
	c.send("/quit\n")
	c.finish(t)
}

func TestChatBlockedHostNeedsAdminAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.networkForbidden = true
	h.f.sessions = append(h.f.sessions, session("ses_a", "t", "running", 0, "acme/api", 1))
	h.f.ws["ses_a"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "sandbox.egress_blocked", pl{"host": "ramp.com", "method": "GET", "runId": "r", "reason": "x", "message": "blocked"}),
		ev(2, "sandbox.egress_blocked", pl{"host": "other.com", "method": "GET", "runId": "r", "reason": "x", "message": "blocked"})}}}
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Allow ramp.com for this session")
	c.send("y\n")
	c.waitFor(t, "You can't decide that here")
	c.send("/quit\n")
	c.finish(t)
	notContains(t, c.out.String(), "Allow other.com") // not asked again after a 403
}

func TestChatSessionsPickByNumber(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	c := h.startChat(false, "chat")
	c.send("/sessions\n")
	c.waitFor(t, "Pick a session by number")
	c.send("1\n")
	c.waitFor(t, "Session ses_a")
	c.send("/switch ses_b\n")
	c.waitFor(t, "Session ses_b")
	c.send("/quit\n")
	eq(t, c.finish(t), 0, "exit")
}

func TestChatExportWritesTheRow(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	dir := t.TempDir()
	wd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(wd) }()
	c := h.startChat(false, "chat", "ses_a")
	c.waitFor(t, "Session ses_a")
	c.send("/export\n")
	c.waitFor(t, "Saved waniis-ses_a.csv")
	b, err := os.ReadFile("waniis-ses_a.csv")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(b), "ses_a")
	c.send("/quit\n")
	c.finish(t)
}

func TestChatRawModeMenuTabAndCtrlC(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	c := h.startChat(true, "chat", "ses_a")
	c.waitFor(t, "Session ses_a")
	c.send("/")
	c.waitFor(t, "/steer <text>") // the menu lists every command under the input line
	c.send("ste")
	c.send("\t") // completes to "/steer "
	c.send("be careful")
	c.waitFor(t, "/steer be careful")
	c.send("\x03") // clears the text
	c.send("\x03") // empty: stop following
	c.waitFor(t, "Stopped following")
	c.send("\x03") // leave
	eq(t, c.finish(t), 0, "exit")
	out := c.out.String()
	contains(t, out, "\r\x1b[J")
	contains(t, out, "reopen with `waniis chat ses_a`")
	eq(t, len(h.f.commandsOf("run.steer")), 0, "nothing sent")
}

func TestChatRawModeEnterRunsHighlightedCommandAndArrowsPick(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedChat(h)
	c := h.startChat(true, "chat", "ses_a")
	c.waitFor(t, "Session ses_a")
	c.send("/qui")
	c.send("\r")
	eq(t, c.finish(t), 0, "/quit left")
	contains(t, c.out.String(), "reopen with")
}

func TestChatRawModeCtrlDLeaves(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	c := h.startChat(true, "chat")
	c.send("\x04")
	eq(t, c.finish(t), 0, "exit")
	contains(t, c.out.String(), "Bye.")
}

func TestBareWaniisShowsHelpWithoutATerminal(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run()
	eq(t, code, 0, "exit")
	contains(t, out, "Usage:")
	contains(t, out, "chat ")
	contains(t, out, "logs ")
}

func TestBareWaniisOpensTheChatInATerminal(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	c := &chatRun{}
	_ = c
	r := h.startChat(true)
	r.waitFor(t, "chat. Type your first message")
	r.send("\x04")
	eq(t, r.finish(t), 0, "exit")
}
