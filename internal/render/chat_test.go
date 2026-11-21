package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
)

var idPattern = regexp.MustCompile(`usr_|ses_|run_|prm_`)

func evAt(seq int, at, typ string, payload any) api.Event {
	e := ev(seq, typ, payload)
	e.At = at
	return e
}

func TestPeopleAreNamedNeverShownAsIDs(t *testing.T) {
	people := NewPeople("usr_01m7e6f4jtzy6h2ktdrdt4anaf", map[string]string{
		"usr_01sara0000000000000000sara": "Sara Lee",
		"usr_01bad00000000000000000bad1": "usr_01bad00000000000000000bad1", // the people route's fallback: the id as the name
	})
	for id, want := range map[string]string{
		"usr_01m7e6f4jtzy6h2ktdrdt4anaf": "you",
		"usr_01sara0000000000000000sara": "Sara Lee",
		"usr_01bad00000000000000000bad1": "someone",
		"usr_01nobody0000000000000nobd":  "someone",
		"":                               "someone",
		"rafiiq":                         "Waniis",
		"waniis":                         "Waniis",
	} {
		if got := people.Name(id); got != want {
			t.Errorf("Name(%q) = %q, want %q", id, got, want)
		}
	}
	if FromPerson("usr_01bad00000000000000000bad1", "sam") != "sam" || FromPerson("Sam R", "sam") != "Sam R" || FromPerson("", "") != "" {
		t.Fatal("display name, then login, never an id")
	}
	if got := people.Unknown(ev(1, "prompt.queued", m{"authorId": "usr_01nobody0000000000000nobd", "text": "x"})); len(got) != 1 {
		t.Fatalf("an event mentioning someone unknown says so: %v", got)
	}
	if got := people.Unknown(ev(1, "prompt.queued", m{"authorId": "usr_01sara0000000000000000sara"})); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestOwnersExampleReadsOnceWithNamesAndNoIDs(t *testing.T) {
	// What the owner typed in the chat: "abcabc", answered by Waniis.
	const me = "usr_01m7e6f4jtzy6h2ktdrdt4anaf"
	text := `I see "abcabc." If you're testing the chat, it's working—what can I help you with?`
	events := []api.Event{
		evAt(1, "2026-11-20T10:00:00Z", "chat.message", m{"messageId": "m1", "authorId": me, "text": "abcabc", "kind": "ask", "offRecord": false}),
		evAt(2, "2026-11-20T10:00:00Z", "prompt.queued", m{"promptId": "prm_1", "authorId": me, "text": "abcabc", "mode": "ask", "position": 0}),
		evAt(3, "2026-11-20T10:00:01Z", "prompt.started", m{"promptId": "prm_1", "runId": "run_1"}),
		evAt(4, "2026-11-20T10:00:02Z", "agent.message.delta", m{"runId": "run_1", "text": `I see "abcabc." `}),
		evAt(5, "2026-11-20T10:00:03Z", "agent.message.delta", m{"runId": "run_1", "text": `If you're testing the chat, it's working—what can I help you with?`}),
		evAt(6, "2026-11-20T10:00:06Z", "agent.message", m{"runId": "run_1", "text": text}),
		evAt(7, "2026-11-20T10:00:07Z", "run.completed", m{"runId": "run_1", "outcome": "success", "summary": text}),
	}
	own := NewOwn(nil)
	own.Expect("abcabc")
	var b strings.Builder
	r := New(&b, Options{Live: true, Chat: true, Own: own, People: NewPeople(me, nil)})
	for _, e := range events {
		r.Event(e)
	}
	r.Flush()
	got := b.String()
	want := "Waniis: " + text + "\n✓ done · 6s\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if idPattern.MatchString(got) {
		t.Fatalf("an id is shown: %s", got)
	}
}

func TestOwnPromptIsMatchedByIDFromTheReply(t *testing.T) {
	own := NewOwn(nil)
	own.Sent("prm_9")
	var b strings.Builder
	r := New(&b, Options{Chat: true, Own: own, People: NewPeople("usr_me00000000000000000000", nil)})
	// A different author and different words: only the prompt id says it is ours.
	r.Event(ev(1, "prompt.queued", m{"promptId": "prm_9", "authorId": "usr_other00000000000000000", "text": "typed", "mode": "build", "position": 0}))
	if b.String() != "" {
		t.Fatalf("shown again: %q", b.String())
	}
}

func TestOwnMatchNeedsTheSameAuthorAndRecentText(t *testing.T) {
	clk := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }
	own := NewOwn(now)
	own.Expect("fix it")
	people := NewPeople("usr_me00000000000000000000", map[string]string{"usr_sara0000000000000000000": "Sara"})
	var b strings.Builder
	r := New(&b, Options{Chat: true, Own: own, People: people})
	// Sara says the same words: shown, as hers.
	r.Event(ev(1, "chat.message", m{"authorId": "usr_sara0000000000000000000", "text": "fix it", "kind": "chat", "offRecord": false}))
	if b.String() != "› Sara: fix it\n" {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	// Two minutes later the same words from you are a new message.
	clk = clk.Add(3 * time.Minute)
	r.Event(ev(2, "chat.message", m{"authorId": "usr_me00000000000000000000", "text": "fix it", "kind": "chat", "offRecord": false}))
	if b.String() != "› you: fix it\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestOthersMessagesShowOnceWhetherChatOrPrompt(t *testing.T) {
	people := NewPeople("usr_me00000000000000000000", map[string]string{"usr_sara0000000000000000000": "Sara"})
	got := run(Options{People: people},
		ev(1, "chat.message", m{"authorId": "usr_sara0000000000000000000", "text": "any luck?", "kind": "ask", "offRecord": false}),
		ev(2, "prompt.queued", m{"promptId": "prm_1", "authorId": "usr_sara0000000000000000000", "text": "any luck?", "mode": "ask", "position": 0}),
		ev(3, "prompt.queued", m{"promptId": "prm_2", "authorId": "usr_sara0000000000000000000", "text": "and tests?", "mode": "build", "position": 1}),
		ev(4, "prompt.queued", m{"promptId": "prm_3", "authorId": "usr_sara0000000000000000000", "text": "plan it", "mode": "plan", "position": 0}),
		ev(5, "prompt.queued", m{"promptId": "prm_4", "authorId": "usr_sara0000000000000000000", "text": "why?", "mode": "ask", "askOnly": true, "position": 0}),
		ev(6, "prompt.queued", m{"promptId": "prm_5", "authorId": "usr_sara0000000000000000000", "text": "handed over", "mode": "build", "position": 0, "continues": "prm_1"}))
	want := "› Sara: any luck?\n› Sara: and tests?  [queued, position 1]\n› Sara: plan it  [plan]\n› Sara: why?  [ask]\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestChatRunEndsAreOneQuietLine(t *testing.T) {
	end := func(outcome, summary string) []api.Event {
		return []api.Event{
			evAt(1, "2026-11-20T10:00:00Z", "prompt.started", m{"promptId": "p", "runId": "r"}),
			evAt(2, "2026-11-20T10:01:12Z", "run.completed", m{"runId": "r", "outcome": outcome, "summary": summary, "costUsd": 0.42}),
		}
	}
	for _, c := range []struct{ outcome, summary, want string }{
		{"success", "", "✓ done · 1m 12s · $0.42\n"},
		{"success", "Fixed the race in the retry loop", "Summary: Fixed the race in the retry loop\n✓ done · 1m 12s · $0.42\n"},
		{"failed", "tests broke", "✗ failed · tests broke · 1m 12s · $0.42\n"},
		{"stopped", "", "■ stopped · 1m 12s · $0.42\n"},
		{"needs_input", "", "? needs your input · 1m 12s · $0.42\n"},
	} {
		if got := run(Options{Chat: true}, end(c.outcome, c.summary)...); got != c.want {
			t.Errorf("%s: got %q want %q", c.outcome, got, c.want)
		}
	}
}

func TestAnswerIsNotRepeatedByTheRunSummary(t *testing.T) {
	events := []api.Event{
		ev(1, "agent.message.delta", m{"runId": "r", "text": "The lockfile "}),
		ev(2, "agent.message.delta", m{"runId": "r", "text": "drifted, so CI is red."}),
		ev(3, "agent.message", m{"runId": "r", "text": "The lockfile drifted, so CI is red."}),
		ev(4, "run.completed", m{"runId": "r", "outcome": "success", "summary": "The lockfile drifted,  so CI is red."}),
	}
	for name, o := range map[string]Options{"live chat": {Live: true, Chat: true}, "logs": {}, "live logs": {Live: true}} {
		got := run(o, events...)
		if strings.Count(got, "lockfile") != 1 {
			t.Errorf("%s: the answer shows once:\n%s", name, got)
		}
	}
	if got := run(Options{}, events...); !strings.HasSuffix(got, "■ run completed\n") {
		t.Errorf("logs keep the classic end line, without the repeated text: %q", got)
	}
	// After tool work the summary says something new: it is shown, labelled.
	got := run(Options{Live: true, Chat: true},
		ev(1, "agent.message", m{"runId": "r", "text": "On it."}),
		ev(2, "run.completed", m{"runId": "r", "outcome": "success", "summary": "Fixed the retry loop and added a test."}))
	if !strings.Contains(got, "Summary: Fixed the retry loop and added a test.\n✓ done") {
		t.Errorf("%q", got)
	}
}

func TestSteeringByYouIsNotEchoedInTheChat(t *testing.T) {
	people := NewPeople("usr_me00000000000000000000", map[string]string{"usr_sara0000000000000000000": "Sara"})
	got := run(Options{Chat: true, People: people},
		ev(1, "run.steered", m{"runId": "r", "authorId": "usr_me00000000000000000000", "text": "slower"}),
		ev(2, "run.steered", m{"runId": "r", "authorId": "usr_sara0000000000000000000", "text": "no migrations"}))
	if got != "↪ Sara steered: no migrations\n" {
		t.Fatalf("%q", got)
	}
}

// everything is a transcript with an id in every field an event can carry one.
func everything() []api.Event {
	const me, sara = "usr_01m7e6f4jtzy6h2ktdrdt4anaf", "usr_01sara0000000000000000sara"
	return []api.Event{
		evAt(1, "2026-11-20T10:00:00Z", "session.created", m{"title": "Checkout", "stage": "working", "createdBy": me}),
		evAt(2, "2026-11-20T10:00:00Z", "participant.joined", m{"userId": sara}),
		evAt(3, "2026-11-20T10:00:00Z", "session.owner_changed", m{"to": "usr_01unknown00000000000000x"}),
		evAt(4, "2026-11-20T10:00:01Z", "chat.message", m{"messageId": "msg_1", "authorId": sara, "text": "please fix checkout", "kind": "ask", "offRecord": false}),
		evAt(5, "2026-11-20T10:00:01Z", "prompt.queued", m{"promptId": "prm_1", "authorId": sara, "text": "please fix checkout", "mode": "build", "position": 0}),
		evAt(6, "2026-11-20T10:00:02Z", "prompt.started", m{"promptId": "prm_1", "runId": "run_1"}),
		evAt(7, "2026-11-20T10:00:03Z", "agent.message.delta", m{"runId": "run_1", "text": "Looking at the retry loop."}),
		evAt(8, "2026-11-20T10:00:04Z", "tool.call", m{"runId": "run_1", "toolCallId": "tc_1", "tool": "github.get_file", "args": m{"path": "src/x.ts"}}),
		evAt(9, "2026-11-20T10:00:04Z", "tool.result", m{"toolCallId": "tc_1", "status": "ok", "summary": "412 lines"}),
		evAt(10, "2026-11-20T10:00:05Z", "run.steered", m{"runId": "run_1", "authorId": me, "text": "careful"}),
		evAt(11, "2026-11-20T10:00:05Z", "run.steered", m{"runId": "run_1", "authorId": sara, "text": "and tests"}),
		evAt(12, "2026-11-20T10:00:06Z", "mcp.approval_requested", m{"requestId": "req_1", "server": "github", "tool": "merge_pr", "risk": "write", "args": m{}}),
		evAt(13, "2026-11-20T10:00:06Z", "network.access_requested", m{"requestId": "req_2", "host": "ramp.com", "access": "read", "reason": "docs", "runId": "run_1"}),
		evAt(14, "2026-11-20T10:00:07Z", "network.host_allowed", m{"host": "ramp.com", "access": "read", "scope": "session", "by": sara}),
		evAt(15, "2026-11-20T10:00:07Z", "network.admin_asked", m{"host": "ramp.com", "by": "usr_01unknown00000000000000x"}),
		evAt(16, "2026-11-20T10:00:08Z", "run.stop_requested", m{"runId": "run_1", "requestedBy": me}),
		evAt(17, "2026-11-20T10:00:08Z", "plan.approved", m{"approvedBy": sara}),
		evAt(18, "2026-11-20T10:00:08Z", "prompt.edited", m{"promptId": "prm_1", "text": "x", "editedBy": sara}),
		evAt(19, "2026-11-20T10:00:09Z", "child.spawned", m{"childId": "ses_child000000000000000", "title": "Look at logs"}),
		evAt(20, "2026-11-20T10:00:09Z", "child.completed", m{"childId": "ses_child000000000000000", "status": "answered", "summary": "found it"}),
		evAt(21, "2026-11-20T10:00:09Z", "session.continued_from", m{"sessionId": "ses_old0000000000000000000", "title": "Earlier", "by": me}),
		evAt(22, "2026-11-20T10:00:09Z", "session.continued_in", m{"sessionId": "ses_new0000000000000000000"}),
		evAt(23, "2026-11-20T10:00:10Z", "changes.publish_requested", m{"requestedBy": me}),
		evAt(24, "2026-11-20T10:00:10Z", "changes.discard_requested", m{"requestedBy": "usr_01unknown00000000000000x"}),
		evAt(25, "2026-11-20T10:00:11Z", "engineer_review.requested", m{"requestedBy": sara}),
		evAt(26, "2026-11-20T10:00:12Z", "run.completed", m{"runId": "run_1", "outcome": "success", "summary": "Fixed the race", "costUsd": 0.42}),
	}
}

func transcriptOf(o Options) string {
	o.People = NewPeople("usr_01m7e6f4jtzy6h2ktdrdt4anaf", map[string]string{"usr_01sara0000000000000000sara": "Sara"})
	return run(o, everything()...)
}

func TestNoIDEverAppearsInHumanOutput(t *testing.T) {
	for name, o := range map[string]Options{
		"logs": {}, "live": {Live: true}, "chat": {Live: true, Chat: true}, "chat history": {Chat: true},
		"timestamps": {Timestamps: true},
	} {
		got := transcriptOf(o)
		if loc := idPattern.FindStringIndex(got); loc != nil {
			t.Errorf("%s shows an id near %q:\n%s", name, got[max(0, loc[0]-20):min(len(got), loc[1]+20)], got)
		}
		if strings.Contains(got, "msg_") || strings.Contains(got, "req_") || strings.Contains(got, "tc_") {
			t.Errorf("%s shows an internal id:\n%s", name, got)
		}
	}
	// Even a bare id as the only information about a person reads "someone".
	got := run(Options{}, ev(1, "participant.left", m{"userId": "usr_01nobody0000000000000nobd"}))
	if got != "● someone left\n" {
		t.Fatalf("%q", got)
	}
}

func TestTranscriptGoldens(t *testing.T) {
	golden(t, "logs_names", transcriptOf(Options{}))
	golden(t, "chat_names", transcriptOf(Options{Live: true, Chat: true}))
}

func TestEveryGoldenIsFreeOfIDs(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no goldens: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if idPattern.Match(b) {
			t.Errorf("%s contains an id: %s", f, idPattern.Find(b))
		}
	}
}

func TestReadyChangesSayHowToOpenAPullRequest(t *testing.T) {
	ready := run(Options{}, ev(1, "changes.kept", m{"runId": "run_1", "files": 2, "additions": 5, "deletions": 1, "ready": true}))
	if !strings.Contains(ready, "changes ready: 2 files, +5 −1") || !strings.Contains(ready, "/pr opens a pull request") {
		t.Fatalf("ready changes: %q", ready)
	}
	kept := run(Options{}, ev(2, "changes.kept", m{"runId": "run_1", "files": 1, "additions": 1, "deletions": 0}))
	if !strings.Contains(kept, "changes kept, not published") || strings.Contains(kept, "/pr") {
		t.Fatalf("kept changes: %q", kept)
	}
}
