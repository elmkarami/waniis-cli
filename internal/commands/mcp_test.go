package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpClient connects an in-memory MCP client to the CLI's server over the fake API.
func (h *harness) mcpClient(readOnly bool) *mcp.ClientSession {
	h.t.Helper()
	d := Deps{In: strings.NewReader(""), Out: &strings.Builder{}, Err: &strings.Builder{},
		Getenv: func(k string) string { return h.env[k] }, HTTP: h.f.srv.Client(), Store: h.store,
		Sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() }, Now: func() time.Time { return h.now },
		NewKey: h.newKey, GitOrigin: func() string { return h.git }, PollInterval: time.Millisecond, RetryDelay: time.Millisecond}
	_, a := build(d)
	a.g.APIURL = h.f.srv.URL
	a.g.Timeout = 5 * time.Second
	a.g.Charts = "auto"
	rt, err := a.newClient(true)
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = a.mcpServer(rt, readOnly).Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	var out map[string]any
	if !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return out, text, res.IsError
}

func TestMCPToolList(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	names := func(cs *mcp.ClientSession) []string {
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, tool := range res.Tools {
			n = append(n, tool.Name)
		}
		return n
	}
	all := names(h.mcpClient(false))
	eq(t, len(all), 9, "tools")
	for _, want := range []string{"list_sessions", "search_sessions", "start_session", "session_status", "session_logs", "prompt_session", "steer_session", "stop_session", "wait_session"} {
		found := false
		for _, n := range all {
			found = found || n == want
		}
		if !found {
			t.Errorf("missing tool %s in %v", want, all)
		}
	}
	ro := names(h.mcpClient(true))
	eq(t, len(ro), 5, "read-only tools")
	for _, n := range ro {
		if n == "start_session" || n == "prompt_session" || n == "steer_session" || n == "stop_session" {
			t.Errorf("read-only server offers %s", n)
		}
	}
}

func TestMCPListAndSearch(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 3)
	h.f.interpreted = map[string]any{"repos": []string{"acme/api"}, "text": "flaky"}
	cs := h.mcpClient(false)
	out, _, isErr := call(t, cs, "list_sessions", map[string]any{"status": []string{"idle"}, "limit": 2, "hasPr": true})
	if isErr {
		t.Fatal("error")
	}
	items := out["items"].([]any)
	eq(t, len(items), 2, "limit honoured")
	eq(t, items[0].(map[string]any)["url"].(string), h.f.srv.URL+"/s/ses_001", "url")
	q := h.f.last("GET", "/v1/sessions").Query
	eq(t, q["status"][0], "idle", "status filter")
	eq(t, q["hasPr"][0], "true", "hasPr")
	out, _, _ = call(t, cs, "search_sessions", map[string]any{"query": "flaky tests"})
	contains(t, out["interpreted"].(string), "acme/api")
	_, text, isErr := call(t, cs, "search_sessions", map[string]any{"query": "  "})
	if !isErr {
		t.Fatalf("empty query should fail: %s", text)
	}
}

func TestMCPStartWithWait(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.ws["ses_01"] = []wsScript{{hold: true, events: []map[string]any{
		ev(1, "prompt.queued", pl{"promptId": "p1", "authorId": "usr_1", "text": "go", "mode": "build", "position": 0}),
		ev(2, "prompt.started", pl{"promptId": "p1", "runId": "r1"}),
		ev(3, "pr.opened", pl{"provider": "github", "url": "https://github.com/acme/api/pull/9", "number": 9, "repo": "acme/api"}),
		runDone(4, "success")}}}
	cs := h.mcpClient(false)
	out, _, isErr := call(t, cs, "start_session", map[string]any{"prompt": "fix it", "repo": "acme/api", "wait": true, "timeoutSeconds": 5})
	if isErr {
		t.Fatal("error")
	}
	eq(t, out["id"], "ses_01", "id")
	eq(t, out["outcome"], "success", "outcome")
	eq(t, out["summary"], "did the thing", "summary")
	eq(t, len(out["pullRequests"].([]any)), 1, "pr")
	eq(t, h.f.last("POST", "/v1/sessions").Body["prompt"].(map[string]any)["source"], "cli", "source")

	_, text, isErr := call(t, cs, "start_session", map[string]any{"prompt": "x"})
	if !isErr {
		t.Fatalf("no repo should fail: %s", text)
	}
	contains(t, text, "exit code 2")
	out, _, _ = call(t, cs, "start_session", map[string]any{"prompt": "chat about it", "chat": true})
	eq(t, h.f.last("POST", "/v1/sessions").Body["stage"], "chat", "chat stage")
	notContains(t, text, "waniis_pat_")
	_ = out
}

func TestMCPStatusLogsAndWait(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seedRun(h, "ses_1")
	h.f.addEvents("ses_1", runDone(5, "failed"))
	cs := h.mcpClient(false)
	out, _, _ := call(t, cs, "session_status", map[string]any{"id": "ses_1"})
	eq(t, out["activity"], "idle", "activity")
	eq(t, out["lastRun"].(map[string]any)["outcome"], "failed", "last run")

	out, _, _ = call(t, cs, "session_logs", map[string]any{"id": "ses_1", "tail": 3})
	eq(t, out["count"].(float64), 3.0, "count")
	eq(t, out["lastSeq"].(float64), 5.0, "lastSeq")
	contains(t, out["text"].(string), "■ run failed")
	notContains(t, out["text"].(string), "\x1b[")
	out, _, _ = call(t, cs, "session_logs", map[string]any{"id": "ses_1", "since": 3, "format": "events"})
	eq(t, len(out["events"].([]any)), 2, "events after 3")
	_, _, isErr := call(t, cs, "session_logs", map[string]any{"id": "ses_1", "format": "xml"})
	if !isErr {
		t.Fatal("bad format")
	}

	// A failed run is a result, not a tool error.
	out, _, isErr = call(t, cs, "wait_session", map[string]any{"id": "ses_1"})
	if isErr {
		t.Fatal("failed run should be a result")
	}
	eq(t, out["outcome"], "failed", "outcome")

	h.f.addEvents("ses_2", ev(1, "prompt.queued", pl{"promptId": "p", "authorId": "u", "text": "x", "mode": "build", "position": 0}))
	out, _, _ = call(t, cs, "wait_session", map[string]any{"id": "ses_2", "timeoutSeconds": 1})
	eq(t, out["timedOut"], true, "timed out is reported, not raised")
}

func TestMCPPromptSteerStop(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.running["ses_1"] = true
	cs := h.mcpClient(false)
	out, _, _ := call(t, cs, "prompt_session", map[string]any{"id": "ses_1", "text": "also this", "mode": "plan"})
	eq(t, out["queued"], true, "queued")
	eq(t, out["promptId"], "prm_1", "prompt id")
	eq(t, h.f.commandsOf("prompt.submit")[0]["mode"], "plan", "mode")

	h.f.status["ses_1"] = pl{"stage": "working", "activity": "running", "run": pl{"id": "r1"}, "queued": 0, "lastSeq": 1}
	out, _, _ = call(t, cs, "steer_session", map[string]any{"id": "ses_1", "text": "careful"})
	eq(t, out["steered"], true, "steered")
	h.f.status["ses_1"] = pl{"stage": "working", "activity": "idle", "run": nil, "queued": 0, "lastSeq": 1}
	_, text, isErr := call(t, cs, "steer_session", map[string]any{"id": "ses_1", "text": "careful"})
	if !isErr {
		t.Fatal("steering an idle session should fail")
	}
	contains(t, text, "prompt")
	out, _, _ = call(t, cs, "stop_session", map[string]any{"id": "ses_1"})
	eq(t, out["stopped"], true, "stopped")
	eq(t, len(h.f.commandsOf("run.stop")), 1, "stop sent")
}

func TestMCPNotSignedInIsAToolError(t *testing.T) {
	h := newHarness(t) // no sign-in
	cs := h.mcpClient(false)
	_, text, isErr := call(t, cs, "list_sessions", nil)
	if !isErr {
		t.Fatal("expected an error result")
	}
	contains(t, text, "waniis login")
	contains(t, text, "exit code 3")
}
