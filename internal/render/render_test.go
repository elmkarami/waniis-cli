package render

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/style"
)

func ev(seq int, typ string, payload any) api.Event {
	b, _ := json.Marshal(payload)
	return api.Event{Seq: seq, Type: typ, At: "2026-11-20T10:00:05Z", Payload: b}
}

func run(o Options, events ...api.Event) string {
	var b strings.Builder
	r := New(&b, o)
	for _, e := range events {
		r.Event(e)
	}
	r.Flush()
	return b.String()
}

type m = map[string]any

func TestTranscript(t *testing.T) {
	events := []api.Event{
		ev(1, "prompt.queued", m{"promptId": "p1", "authorId": "usr_1", "text": "fix the flaky test", "mode": "build", "position": 0}),
		ev(2, "prompt.started", m{"promptId": "p1", "runId": "r1"}),
		ev(3, "agent.message.delta", m{"runId": "r1", "text": "Looking "}),
		ev(4, "agent.message.delta", m{"runId": "r1", "text": "at it."}),
		ev(5, "agent.message", m{"runId": "r1", "text": "Looking at it."}),
		ev(6, "tool.call", m{"runId": "r1", "toolCallId": "t1", "tool": "github.get_file", "args": m{"path": "src/x.ts"}}),
		ev(7, "tool.result", m{"toolCallId": "t1", "status": "ok", "summary": "412 lines"}),
		ev(8, "sandbox.egress_blocked", m{"host": "ramp.com", "method": "GET", "runId": "r1", "reason": "not_allowed", "message": "not allowed"}),
		ev(9, "network.access_requested", m{"requestId": "n1", "host": "ramp.com", "access": "read", "reason": "docs", "runId": "r1", "expiresAt": "2026-11-20T11:00:00Z"}),
		ev(10, "verification.completed", m{"runId": "r1", "attempt": 1, "overall": "verified", "fixing": false, "proofs": []any{m{"bundle": m{"checks": []any{
			m{"kind": "tests", "status": "pass", "counts": m{"passed": 24}}, m{"kind": "lint", "status": "pass"}, m{"kind": "typecheck", "status": "pass"}}}}}}),
		ev(11, "pr.opened", m{"provider": "github", "url": "https://github.com/acme/api/pull/318", "number": 318, "repo": "acme/api"}),
		ev(12, "pr.updated", m{"repo": "acme/api", "number": 318, "url": "u", "state": "merged", "by": "maya"}),
		ev(13, "run.completed", m{"runId": "r1", "outcome": "success", "summary": "Fixed the race", "costUsd": 0.42}),
		ev(14, "chat.edited", m{}),
	}
	got := run(Options{Names: map[string]string{"usr_1": "maya"}}, events...)
	for _, want := range []string{
		"› maya: fix the flaky test",
		"▶ run started",
		"Waniis: Looking at it.",
		"⚙ tool github.get_file src/x.ts ✓",
		"⛔ blocked ramp.com (GET)",
		"⏸ needs approval: network access to ramp.com (read) — docs",
		"✓ checks: tests 24 passed, lint, typecheck",
		"PR acme/api#318 opened https://github.com/acme/api/pull/318",
		"PR #318 merged by maya",
		"■ run completed: Fixed the race ($0.42)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "Looking") != 1 {
		t.Errorf("deltas and the final message should print once:\n%s", got)
	}
	if strings.Contains(got, "chat.edited") {
		t.Errorf("quiet events printed:\n%s", got)
	}
	golden(t, "transcript", got)
}

func TestFailedRunStoppedAndNeedsInput(t *testing.T) {
	got := run(Options{},
		ev(1, "run.completed", m{"runId": "r", "outcome": "failed", "summary": "tests broke"}),
		ev(2, "run.completed", m{"runId": "r", "outcome": "stopped", "summary": ""}),
		ev(3, "run.completed", m{"runId": "r", "outcome": "needs_input", "summary": "which db?"}))
	for _, want := range []string{"■ run failed: tests broke", "■ run stopped", "■ run needs input: which db?"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestLiveDeltasStreamAndToolsShowAtOnce(t *testing.T) {
	var b strings.Builder
	r := New(&b, Options{Live: true})
	r.Event(ev(1, "agent.message.delta", m{"runId": "r", "text": "Hel"}))
	if b.String() != "Waniis: Hel" {
		t.Fatalf("got %q", b.String())
	}
	r.Event(ev(2, "agent.message.delta", m{"runId": "r", "text": "lo\n"}))
	r.Event(ev(3, "tool.call", m{"runId": "r", "toolCallId": "t", "tool": "bash", "args": m{"command": "go test"}}))
	r.Event(ev(4, "agent.message", m{"runId": "r", "text": "Hello\n"}))
	r.Event(ev(5, "tool.result", m{"toolCallId": "t", "status": "error", "summary": "exit 1"}))
	r.Flush()
	got := b.String()
	if !strings.Contains(got, "Waniis: Hello\n⚙ tool bash go test\n") || !strings.Contains(got, "✗ exit 1") || strings.Count(got, "Hello") != 1 {
		t.Fatalf("got:\n%s", got)
	}
}

func TestUnfinishedDeltasPrintOnFlush(t *testing.T) {
	got := run(Options{}, ev(1, "agent.message.delta", m{"runId": "r", "text": "partial "}), ev(2, "agent.message.delta", m{"runId": "r", "text": "answer"}))
	if got != "Waniis: partial answer\n" {
		t.Fatalf("got %q", got)
	}
}

func TestToolWithoutResult(t *testing.T) {
	got := run(Options{}, ev(1, "tool.call", m{"runId": "r", "toolCallId": "t", "tool": "bash", "args": m{"command": "sleep 99"}}))
	if got != "⚙ tool bash sleep 99 …\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrivatePlaceholder(t *testing.T) {
	e := ev(1, "tool.call", m{"runId": "r", "toolCallId": "t", "tool": "x", "args": m{}})
	e.Private = &api.PrivateMark{ID: "i", Kind: "lookup", OwnerID: "usr_2", OwnerName: "Sam", Server: "gmail", Title: "Gmail", Hidden: true}
	got := run(Options{}, e, ev(2, "tool.result", m{"toolCallId": "t", "status": "ok", "summary": "SECRET"}))
	if !strings.Contains(got, "private lookup from Sam (Gmail)") || strings.Contains(got, "SECRET") {
		t.Fatalf("got %q", got)
	}
}

func TestColorsOnlyWhenAsked(t *testing.T) {
	e := ev(1, "run.completed", m{"runId": "r", "outcome": "failed", "summary": "x"})
	if strings.Contains(run(Options{}, e), "\x1b[") {
		t.Fatal("color without Style.On")
	}
	if !strings.Contains(run(Options{Style: style.Style{On: true}}, e), "\x1b[31m") {
		t.Fatal("expected red")
	}
}

func TestTimestamps(t *testing.T) {
	got := run(Options{Timestamps: true}, ev(1, "run.completed", m{"runId": "r", "outcome": "success", "summary": ""}))
	if !strings.HasPrefix(got, "10:00:05 ■ run completed") {
		t.Fatalf("got %q", got)
	}
}

func TestMultilineAnswerIsIndentedAndHasChart(t *testing.T) {
	text := "Numbers:\n```chart\n" + barJSON + "\n```\n"
	got := run(Options{Width: 80, Charts: true}, ev(1, "agent.message", m{"runId": "r", "text": text}))
	if !strings.HasPrefix(got, "Waniis: Numbers:\n  Refunds by plan") {
		t.Fatalf("got:\n%s", got)
	}
	_ = fmt.Sprint
}

func TestNeverSaysRafiiq(t *testing.T) {
	got := run(Options{}, ev(1, "prompt.queued", m{"promptId": "p", "authorId": "rafiiq", "text": "x", "mode": "ask"}),
		ev(2, "session.repos_set", m{"repos": []string{"a/b"}, "setBy": "rafiiq"}))
	if strings.Contains(strings.ToLower(got), "rafiiq") {
		t.Fatalf("got %q", got)
	}
}
