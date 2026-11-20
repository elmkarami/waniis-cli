package commands

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type pl = map[string]any

func promptStarted(seq int) map[string]any {
	return ev(seq, "prompt.started", pl{"promptId": "p1", "runId": "r1"})
}

func seedRun(h *harness, id string) {
	h.f.addEvents(id,
		ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "usr_1", "text": "fix the flaky test", "mode": "build", "position": 0}),
		promptStarted(2),
		ev(3, "tool.call", pl{"runId": "r1", "toolCallId": "t1", "tool": "github.get_file", "args": pl{"path": "src/x.ts"}}),
		ev(4, "tool.result", pl{"toolCallId": "t1", "status": "ok", "summary": "ok"}),
	)
}

func runDone(seq int, outcome string) map[string]any {
	return ev(seq, "run.completed", pl{"runId": "r1", "outcome": outcome, "summary": "did the thing"})
}

func noSecrets(t *testing.T, outs ...string) {
	t.Helper()
	for _, o := range outs {
		notContains(t, o, "waniis_pat_")
		notContains(t, o, "waniis_cli_")
		notContains(t, strings.ToLower(o), "rafiiq")
	}
}

func TestLogsDefaultsToTail100(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	for i := 1; i <= 150; i++ {
		h.f.addEvents("ses_1", ev(i, "run.stop_requested", pl{"runId": "r", "requestedBy": fmt.Sprintf("u%d", i)}))
	}
	code, out, errOut := h.run("sessions", "logs", "ses_1")
	eq(t, code, 0, "exit")
	eq(t, h.f.last("GET", "/v1/sessions/ses_1/events").Query["tail"][0], "100", "tail asked of the server")
	eq(t, strings.Count(out, "stop requested"), 100, "lines")
	contains(t, out, "u150")
	notContains(t, out, "u50\n")
	noSecrets(t, out, errOut)
}

func TestLogsTailKeepsLastNWhenServerIgnoresTail(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.legacyEvents = true
	for i := 1; i <= 10; i++ {
		h.f.addEvents("ses_1", ev(i, "run.stop_requested", pl{"runId": "r", "requestedBy": fmt.Sprintf("u%d", i)}))
	}
	code, out, _ := h.run("logs", "ses_1", "--tail", "3")
	eq(t, code, 0, "exit")
	eq(t, strings.Count(out, "stop requested"), 3, "lines")
	contains(t, out, "u10")
	notContains(t, out, "u7 ")
}

func TestLogsAllPages(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	for i := 1; i <= 1200; i++ {
		h.f.addEvents("ses_1", ev(i, "run.stop_requested", pl{"runId": "r", "requestedBy": "u"}))
	}
	code, out, _ := h.run("logs", "ses_1", "--all", "--json")
	eq(t, code, 0, "exit")
	eq(t, len(strings.Split(strings.TrimSpace(out), "\n")), 1200, "ndjson lines")
	if n := h.f.count("GET", "/v1/sessions/ses_1/events"); n != 3 {
		t.Fatalf("expected 3 page requests, got %d", n)
	}
}

func TestLogsAllWithLegacyServerStopsAfterOnePage(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.legacyEvents = true
	seedRun(h, "ses_1")
	code, out, _ := h.run("logs", "ses_1", "--all")
	eq(t, code, 0, "exit")
	contains(t, out, "› prompt")
	eq(t, h.f.count("GET", "/v1/sessions/ses_1/events"), 1, "one request")
}

func TestLogsSince(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	code, out, _ := h.run("logs", "ses_1", "--since", "2")
	eq(t, code, 0, "exit")
	contains(t, out, "src/x.ts")
	notContains(t, out, "› prompt")
}

func TestLogsRenderedLines(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya", "name": "Maya L"}}
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1",
		ev(5, "sandbox.egress_blocked", pl{"host": "ramp.com", "method": "GET", "runId": "r1", "reason": "x", "message": "blocked"}),
		runDone(6, "success"))
	code, out, _ := h.run("logs", "ses_1")
	eq(t, code, 0, "exit")
	contains(t, out, "› prompt (maya): fix the flaky test")
	contains(t, out, "▶ run started (build)")
	contains(t, out, "⚙ tool github.get_file src/x.ts ✓")
	contains(t, out, "⛔ blocked ramp.com (GET)")
	contains(t, out, "■ run completed: did the thing")
	notContains(t, out, "\x1b[")
}

func TestLogsNDJSONKeepsServerFields(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	e := runDone(1, "success")
	e["futureField"] = "kept"
	h.f.addEvents("ses_1", e)
	code, out, _ := h.run("--json", "logs", "ses_1")
	eq(t, code, 0, "exit")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	eq(t, len(lines), 1, "one line per event")
	m := mustJSON(t, lines[0])
	eq(t, m["futureField"], "kept", "passthrough")
	eq(t, m["type"], "run.completed", "type")
}

func TestLogsRejectsBadFlags(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	for _, args := range [][]string{
		{"logs", "ses_1", "--all", "--tail", "5"}, {"logs", "ses_1", "--until", "never"}, {"logs", "ses_1", "--tail", "0"},
		{"--charts", "sometimes", "logs", "ses_1"},
	} {
		code, _, _ := h.run(args...)
		eq(t, code, 2, strings.Join(args, " "))
	}
}

func TestLogsChartDrawnWithAlwaysAndAsTableWithNever(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	text := "Here:\n```chart\n{\"kind\":\"bar\",\"title\":\"Refunds\",\"x\":[\"a\",\"b\"],\"series\":[{\"name\":\"S\",\"values\":[10,5]}]}\n```\n"
	h.f.addEvents("ses_1", ev(1, "agent.message", pl{"runId": "r", "text": text}))
	_, out, _ := h.run("--charts", "always", "logs", "ses_1")
	contains(t, out, "█")
	contains(t, out, "Refunds")
	_, out, _ = h.run("--charts", "never", "logs", "ses_1")
	notContains(t, out, "█")
	contains(t, out, "Refunds")
	contains(t, out, "10")
	_, out, _ = h.run("--json", "logs", "ses_1")
	contains(t, out, "```chart") // JSON mode doesn't draw
}

func TestFollowStreamsOverWebSocketWithBearer(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{hold: true,
		frames: []string{`{"type":"presence.snapshot","people":[]}`},
		events: []map[string]any{ev(5, "agent.message", pl{"runId": "r1", "text": "Done thinking."}), runDone(6, "success")}}}
	code, out, errOut := h.run("logs", "ses_1", "--follow", "--until", "run-end")
	eq(t, code, 0, "exit")
	contains(t, out, "Waniis: Done thinking.")
	contains(t, out, "■ run completed")
	conns := h.f.wsConns()
	eq(t, len(conns), 1, "one connection")
	eq(t, conns[0].Since, 4, "resumes after the history")
	eq(t, conns[0].Auth, "Bearer "+testPAT, "bearer on the upgrade")
	eq(t, conns[0].Query["client"][0], "cli", "client")
	noSecrets(t, out, errOut)
}

func TestFollowReconnectsFromLastSeq(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{
		{events: []map[string]any{ev(5, "agent.message", pl{"runId": "r1", "text": "first part"})}}, // then drops
		{hold: true, events: []map[string]any{ev(5, "agent.message", pl{"runId": "r1", "text": "first part"}), runDone(6, "success")}},
	}
	code, out, errOut := h.run("logs", "ses_1", "-f", "--until", "run-end")
	eq(t, code, 0, "exit")
	eq(t, strings.Count(out, "first part"), 1, "no duplicate after the replay")
	contains(t, out, "■ run completed")
	contains(t, errOut, "reconnecting")
	conns := h.f.wsConns()
	eq(t, len(conns), 2, "reconnected")
	eq(t, conns[1].Since, 5, "resumed from the last seq")
	if len(h.sleeps) == 0 {
		t.Fatal("expected a backoff sleep")
	}
}

func TestFollowFallsBackToPolling(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{status: 426}, {status: 426}, {status: 426}}
	added := false
	code, out, errOut := h.runWithHook(func() {
		if !added {
			added = true
			h.f.addEvents("ses_1", runDone(5, "success"))
		}
	}, "logs", "ses_1", "--until", "run-end")
	eq(t, code, 0, "exit")
	contains(t, out, "■ run completed")
	contains(t, errOut, "polling")
}

func TestFollowAuthFailureOnUpgrade(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{status: 401}}
	code, _, errOut := h.run("watch", "ses_1")
	eq(t, code, 3, "exit")
	contains(t, errOut, "waniis login")
}

func TestUntilFailedRunExits10(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{hold: true, events: []map[string]any{runDone(5, "failed")}}}
	code, out, errOut := h.run("logs", "ses_1", "--until", "run-end")
	eq(t, code, 10, "exit")
	contains(t, out, "■ run failed")
	contains(t, errOut, "ended failed")
}

func TestUntilIdleWaitsForQueuedPrompts(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1", ev(5, "prompt.queued", pl{"promptId": "p2", "authorId": "usr_1", "text": "more", "mode": "build", "position": 1}))
	h.f.ws["ses_1"] = []wsScript{{hold: true, events: []map[string]any{
		runDone(6, "success"),
		ev(7, "prompt.started", pl{"promptId": "p2", "runId": "r2"}),
		ev(8, "run.completed", pl{"runId": "r2", "outcome": "success", "summary": "second"}),
	}}}
	code, out, _ := h.run("logs", "ses_1", "--until", "idle")
	eq(t, code, 0, "exit")
	contains(t, out, "second")
}

func TestUntilRunEndReturnsAtOnceWhenIdle(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1", runDone(5, "success"))
	code, _, _ := h.run("logs", "ses_1", "--until", "run-end")
	eq(t, code, 0, "exit")
	eq(t, len(h.f.wsConns()), 0, "no socket needed")
}

func TestUntilPROpen(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{hold: true, events: []map[string]any{
		ev(5, "pr.opened", pl{"provider": "github", "url": "https://github.com/acme/api/pull/318", "number": 318, "repo": "acme/api"})}}}
	code, out, _ := h.run("logs", "ses_1", "--until", "pr-open")
	eq(t, code, 0, "exit")
	contains(t, out, "PR acme/api#318 opened")
}

func TestUntilPROpenWithoutPRWhenRunEnds(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{hold: true, events: []map[string]any{runDone(5, "success")}}}
	code, _, errOut := h.run("sessions", "wait", "ses_1", "--until", "pr-open")
	eq(t, code, 1, "exit")
	contains(t, errOut, "without opening a pull request")
}

func TestFollowTimeout(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	code, _, errOut := h.run("sessions", "wait", "ses_1", "--timeout", "100ms")
	eq(t, code, 8, "exit")
	contains(t, errOut, "keeps running")
}

func TestWait(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1", runDone(5, "success"), ev(6, "pr.opened", pl{"provider": "github", "url": "https://github.com/acme/api/pull/3", "number": 3, "repo": "acme/api"}))
	code, out, _ := h.run("--json", "sessions", "wait", "ses_1")
	eq(t, code, 0, "exit")
	m := mustJSON(t, out)
	eq(t, m["outcome"], "success", "outcome")
	eq(t, m["summary"], "did the thing", "summary")
	eq(t, len(m["pullRequests"].([]any)), 1, "prs")

	h2 := newHarness(t)
	h2.signIn()
	seedRun(h2, "ses_1")
	h2.f.addEvents("ses_1", runDone(5, "stopped"))
	code, out, _ = h2.run("sessions", "wait", "ses_1")
	eq(t, code, 10, "stopped exits 10")
	contains(t, out, "Run stopped")

	h3 := newHarness(t)
	h3.signIn()
	code, out, _ = h3.run("sessions", "wait", "ses_1")
	eq(t, code, 0, "nothing ran")
	contains(t, out, "Nothing has run")
}

func TestStatusUsesTheServersAnswer(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.status["ses_1"] = pl{"stage": "working", "activity": "running", "queued": 2,
		"run":     pl{"id": "r9", "promptId": "p9", "stage": "verifying", "mode": "build", "startedAt": "2026-11-20T10:00:00Z"},
		"lastRun": pl{"id": "r8", "outcome": "success", "summary": "earlier work", "at": "2026-11-20T09:00:00Z"}, "serverOnly": "kept",
		"pullRequest": pl{"number": 7, "url": "https://github.com/acme/api/pull/7", "draft": true}, "sandbox": pl{"status": "ready"}, "lastSeq": 40, "at": "2026-11-20T10:01:00Z"}
	code, out, _ := h.run("sessions", "status", "ses_1")
	eq(t, code, 0, "exit")
	contains(t, out, "running")
	contains(t, out, "(draft)")
	contains(t, out, "r9 (verifying, build)")
	contains(t, out, "Queued prompts")
	contains(t, out, "earlier work")
	contains(t, out, "#7")
	contains(t, out, "ready")
	code, out, _ = h.run("--json", "sessions", "status", "ses_1")
	eq(t, code, 0, "exit")
	eq(t, mustJSON(t, out)["serverOnly"], "kept", "json passthrough")
}

func TestStatusDerivedWhenServerHasNoStatusRoute(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1", ev(5, "prompt.queued", pl{"promptId": "p2", "authorId": "usr_1", "text": "x", "mode": "build", "position": 1}),
		ev(6, "verification.started", pl{"runId": "r1", "attempt": 1, "repos": []string{}}))
	code, out, _ := h.run("--json", "sessions", "status", "ses_1")
	eq(t, code, 0, "exit")
	m := mustJSON(t, out)
	eq(t, m["queued"].(float64), 1.0, "queued")
	eq(t, m["run"].(map[string]any)["stage"], "verifying", "stage")
	eq(t, m["activity"], "running", "activity")

	h.f.missing["ses_nope"] = true
	code, _, _ = h.run("sessions", "status", "ses_nope")
	eq(t, code, 4, "a real 404 stays 4")
}

func TestPromptQueuedAndIdempotent(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.running["ses_1"] = true
	h.f.failCommands = []int{503}
	code, out, _ := h.run("sessions", "prompt", "ses_1", "also add a test", "--mode", "build", "--model", "claude-x", "--idempotency-key", "k-1")
	eq(t, code, 0, "exit")
	contains(t, out, "Queued behind the running work (position 1)")
	cmds := h.f.commandsOf("prompt.submit")
	eq(t, len(cmds), 2, "retried once after the 503")
	eq(t, cmds[0]["source"], "cli", "source")
	eq(t, cmds[0]["mode"], "build", "mode")
	eq(t, cmds[0]["model"], "claude-x", "model")
	eq(t, h.f.last("POST", "/v1/sessions/ses_1/commands").Header.Get("Idempotency-Key"), "k-1", "key kept across retries")
}

func TestPromptFromStdinAndJSON(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.in = "rebase on main\n"
	code, out, _ := h.run("--json", "sessions", "prompt", "ses_1", "-")
	eq(t, code, 0, "exit")
	m := mustJSON(t, out)
	eq(t, m["queued"], false, "started at once")
	eq(t, m["promptId"], "prm_1", "prompt id")
	eq(t, h.f.commandsOf("prompt.submit")[0]["text"], "rebase on main", "text from stdin")
	code, _, _ = h.run("sessions", "prompt", "ses_1", "  ")
	eq(t, code, 2, "empty prompt")
}

func TestPromptWaitAndFollow(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1", runDone(5, "success"))
	// The fake queues the prompt as prm_1; its run is the one the log says started p1.
	h.f.ws["ses_1"] = nil
	h.f.addEvents("ses_1",
		ev(6, "prompt.queued", pl{"promptId": "prm_1", "authorId": "usr_1", "text": "more", "mode": "build", "position": 0}),
		ev(7, "prompt.started", pl{"promptId": "prm_1", "runId": "r2"}))
	h.f.ws["ses_1"] = []wsScript{{hold: true, events: []map[string]any{
		ev(8, "agent.message", pl{"runId": "r2", "text": "On it."}),
		ev(9, "run.completed", pl{"runId": "r2", "outcome": "failed", "summary": "broke"})}}}
	code, out, _ := h.run("sessions", "prompt", "ses_1", "more", "--follow")
	eq(t, code, 10, "exit")
	contains(t, out, "On it.")
	contains(t, out, "■ run failed")
	notContains(t, out, "fix the flaky test") // history before the prompt isn't replayed

	h2 := newHarness(t)
	h2.signIn()
	h2.f.addEvents("ses_1", ev(1, "prompt.queued", pl{"promptId": "prm_1", "authorId": "u", "text": "x", "mode": "build", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "prm_1", "runId": "r2"}), ev(3, "run.completed", pl{"runId": "r2", "outcome": "success", "summary": "fine"}))
	code, out, _ = h2.run("--json", "sessions", "prompt", "ses_1", "x", "--wait")
	eq(t, code, 0, "exit")
	eq(t, mustJSON(t, out)["outcome"], "success", "outcome")
}

func TestSteer(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.status["ses_1"] = pl{"stage": "working", "activity": "running", "run": pl{"id": "r1"}, "queued": 0, "lastSeq": 3}
	code, out, _ := h.run("--json", "sessions", "steer", "ses_1", "don't touch migrations")
	eq(t, code, 0, "exit")
	eq(t, mustJSON(t, out)["runId"], "run_7", "run id")
	eq(t, h.f.commandsOf("run.steer")[0]["text"], "don't touch migrations", "text")
	if h.f.last("POST", "/v1/sessions/ses_1/commands").Header.Get("Idempotency-Key") == "" {
		t.Fatal("steer must carry an idempotency key")
	}

	// The status says idle: nothing is sent.
	before := len(h.f.commandsOf("run.steer"))
	h.f.status["ses_1"] = pl{"stage": "working", "activity": "idle", "run": nil, "queued": 0, "lastRun": nil, "pullRequest": nil, "sandbox": nil, "lastSeq": 3}
	code, _, errOut := h.run("sessions", "steer", "ses_1", "x")
	eq(t, code, 5, "nothing running")
	contains(t, errOut, "Nothing is running")
	contains(t, errOut, "waniis sessions prompt ses_1")
	eq(t, len(h.f.commandsOf("run.steer")), before, "no command sent")

	// No status route, and the server refuses the steer: the 4xx becomes the same advice.
	h.f.status = map[string]map[string]any{}
	h.f.noRun = true
	code, _, errOut = h.run("sessions", "steer", "ses_1", "x")
	eq(t, code, 5, "refused")
	contains(t, errOut, "waniis sessions prompt ses_1")
}

func TestStartFollow(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.ws["ses_01"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "usr_1", "text": "go", "mode": "build", "position": 0}),
		ev(2, "agent.message", pl{"runId": "r1", "text": "Working."}),
		ev(3, "pr.opened", pl{"provider": "github", "url": "https://github.com/acme/api/pull/9", "number": 9, "repo": "acme/api"}),
		runDone(4, "success")}}}
	code, out, errOut := h.run("new", "go", "--repo", "acme/api", "--follow")
	eq(t, code, 0, "exit")
	contains(t, out, "Waniis: Working.")
	contains(t, out, "PR acme/api#9 opened")
	contains(t, errOut, "Started session ses_01")
	eq(t, h.f.conns[0].Since, 0, "streams from the start")

	code, _, _ = h.run("new", "go", "--repo", "acme/api", "--follow", "--wait")
	eq(t, code, 2, "--wait and --follow are exclusive")
}

func TestTopLevelShortcuts(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 1)
	code, out, _ := h.run("ls")
	eq(t, code, 0, "ls")
	contains(t, out, "ses_001")
	seedRun(h, "ses_001")
	code, out, _ = h.run("logs", "ses_001")
	eq(t, code, 0, "logs")
	contains(t, out, "› prompt")
	_ = time.Second
}

func TestVerboseNeverPrintsTheToken(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.ws["ses_1"] = []wsScript{{status: 401}}
	_, out, errOut := h.run("--verbose", "logs", "ses_1", "--until", "run-end")
	noSecrets(t, out, errOut)
	_, out, errOut = h.run("--verbose", "sessions", "prompt", "ses_1", "x")
	noSecrets(t, out, errOut)
}

func TestTailAboveServerCapReadsTheLog(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	for i := 1; i <= 1100; i++ {
		h.f.addEvents("ses_1", ev(i, "run.stop_requested", pl{"runId": "r", "requestedBy": "u"}))
	}
	code, out, _ := h.run("logs", "ses_1", "--tail", "1050", "--json")
	eq(t, code, 0, "exit")
	eq(t, len(strings.Split(strings.TrimSpace(out), "\n")), 1050, "lines")
}
