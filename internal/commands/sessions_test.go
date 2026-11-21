package commands

import (
	"fmt"
	"strings"
	"testing"
)

func seed(h *harness, n int) {
	for i := 1; i <= n; i++ {
		pr := 0
		if i%2 == 0 {
			pr = 100 + i
		}
		h.f.sessions = append(h.f.sessions, session(fmt.Sprintf("ses_%03d", i), fmt.Sprintf("Session number %d", i), "idle", pr, "acme/api", i))
	}
}

func TestListTable(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 3)
	h.f.sessions[0]["status"] = "running"
	code, out, _ := h.run("sessions", "list")
	eq(t, code, 0, "exit code")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	eq(t, len(lines), 4, "header plus three rows")
	contains(t, lines[0], "ID")
	contains(t, lines[0], "STATUS")
	contains(t, lines[1], "ses_001")
	contains(t, lines[1], "running")
	contains(t, lines[1], "1m ago")
	contains(t, lines[2], "#102")
	contains(t, lines[2], "acme/api")
	// columns line up
	eq(t, strings.Index(lines[0], "STATUS"), strings.Index(lines[1], "running"), "aligned columns")
}

func TestListSendsFilters(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, _ := h.run("sessions", "list", "--filter", "mine", "--status", "running", "--status", "failed",
		"--repo", "acme/api", "--author", "me", "--stage", "working", "--since", "2026-11-01", "--until", "2026-11-19", "--has-pr", "--limit", "20")
	eq(t, code, 0, "exit code")
	q := h.f.last("GET", "/v1/sessions").Query
	eq(t, q["filter"][0], "mine", "filter")
	eq(t, strings.Join(q["status"], ","), "running,failed", "status")
	eq(t, q["repo"][0], "acme/api", "repo")
	eq(t, q["author"][0], "me", "author")
	eq(t, q["stage"][0], "working", "stage")
	eq(t, q["since"][0], "2026-11-01", "since")
	eq(t, q["until"][0], "2026-11-19", "until")
	eq(t, q["hasPr"][0], "true", "hasPr")
	eq(t, q["limit"][0], "20", "limit")
}

func TestListPagesAndAll(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 7)
	h.f.pageSize = 3

	// One page, with the next cursor told on stderr.
	code, out, errOut := h.run("sessions", "list")
	eq(t, code, 0, "exit code")
	eq(t, strings.Count(out, "ses_"), 3, "one page of rows")
	contains(t, errOut, "--cursor 3")
	eq(t, h.f.count("GET", "/v1/sessions"), 1, "one request")

	// Continue from the cursor.
	code, out, _ = h.run("sessions", "list", "--cursor", "3")
	eq(t, code, 0, "exit code")
	contains(t, out, "ses_004")
	notContains(t, out, "ses_003")

	// --all follows every page.
	before := h.f.count("GET", "/v1/sessions")
	code, out, _ = h.run("--json", "sessions", "list", "--all")
	eq(t, code, 0, "exit code")
	m := mustJSON(t, out)
	items := m["items"].([]any)
	eq(t, len(items), 7, "all sessions")
	eq(t, m["next"], nil, "no next cursor")
	eq(t, h.f.count("GET", "/v1/sessions")-before, 3, "three page requests")
}

func TestListJSONKeepsServerFields(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 1)
	code, out, _ := h.run("sessions", "list", "--json")
	eq(t, code, 0, "exit code")
	m := mustJSON(t, out)
	first := m["items"].([]any)[0].(map[string]any)
	eq(t, first["id"], any("ses_001"), "id")
	eq(t, first["extraServerField"], any("kept"), "unknown server fields pass through")
	if _, ok := m["next"]; !ok {
		t.Fatal("list JSON always has next")
	}
}

func TestListAcceptsTodaysSessionsKey(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	// Today's API answers {"sessions": [...]} with no cursor.
	h.f.sessions = nil
	code, out, _ := h.run("sessions", "list", "--json")
	eq(t, code, 0, "exit code")
	eq(t, len(mustJSON(t, out)["items"].([]any)), 0, "empty list is []")
}

func TestListEmptyAndBadFlags(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, errOut := h.run("sessions", "list")
	eq(t, code, 0, "exit code")
	eq(t, out, "", "no stdout for an empty table")
	contains(t, errOut, "No sessions found")
	code, _, errOut = h.run("sessions", "list", "--filter", "bogus")
	eq(t, code, 2, "usage error")
	contains(t, errOut, "--filter")
	code, _, _ = h.run("sessions", "list", "--nope")
	eq(t, code, 2, "unknown flag is a usage error")
}

func TestListLimitDefaultsAndClamp(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.run("sessions", "list")
	eq(t, h.f.last("GET", "/v1/sessions").Query["limit"][0], "100", "non-terminal default")
	h.tty = true
	h.run("sessions", "list")
	eq(t, h.f.last("GET", "/v1/sessions").Query["limit"][0], "30", "terminal default")
	h.run("sessions", "list", "--limit", "5000")
	eq(t, h.f.last("GET", "/v1/sessions").Query["limit"][0], "100", "clamped to the API's cap")
}

func TestSearchPrintsInterpretation(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 2)
	h.f.interpreted = map[string]any{"repos": []string{"acme/pay"}, "since": "2026-11-11", "prState": "merged", "text": "payment"}
	code, out, errOut := h.run("sessions", "search", "payment bugs fixed last week that have a merged PR")
	eq(t, code, 0, "exit code")
	contains(t, errOut, "Searching: merged PRs in acme/pay since 2026-11-11 matching 'payment'")
	contains(t, out, "ses_001")
	q := h.f.last("GET", "/v1/sessions/search").Query
	eq(t, q["q"][0], "payment bugs fixed last week that have a merged PR", "query text")

	code, out, _ = h.run("--json", "sessions", "search", "payment")
	eq(t, code, 0, "json exit code")
	m := mustJSON(t, out)
	interp := m["interpreted"].(map[string]any)
	eq(t, interp["prState"], any("merged"), "interpreted.prState")
	eq(t, len(m["items"].([]any)), 2, "items")
}

func TestSearchNoInterpretAndFlagsWin(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.interpreted = map[string]any{"text": "x"}
	code, _, errOut := h.run("sessions", "search", "flaky", "--no-interpret", "--repo", "acme/api")
	eq(t, code, 0, "exit code")
	notContains(t, errOut, "Searching:")
	q := h.f.last("GET", "/v1/sessions/search").Query
	eq(t, q["interpret"][0], "false", "interpret=false")
	eq(t, q["repo"][0], "acme/api", "explicit flags are sent")
}

func TestSearchNeedsText(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, _ := h.run("sessions", "search")
	eq(t, code, 2, "missing argument")
	code, _, _ = h.run("sessions", "search", "  ")
	eq(t, code, 2, "blank text")
}

func TestShow(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 2)
	code, out, _ := h.run("sessions", "show", "ses_002")
	eq(t, code, 0, "exit code")
	contains(t, out, "#102")
	contains(t, out, "/s/ses_002")
}

// ---- start

func TestStartSendsKeyedCreateWithFirstPrompt(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, errOut := h.run("sessions", "start", "fix the flaky checkout test", "--repo", "acme/api", "--branch", "dev",
		"--mode", "plan", "--model", "m1", "--title", "Flaky", "--visibility", "workspace")
	eq(t, code, 0, "exit code: "+errOut)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	eq(t, lines[0], "ses_01", "id on the first line")
	contains(t, lines[1], "/s/ses_01")
	r := h.f.last("POST", "/v1/sessions")
	eq(t, r.Header.Get("Idempotency-Key"), "key-1", "idempotency key")
	eq(t, r.Body["title"], any("Flaky"), "title")
	eq(t, r.Body["visibility"], any("workspace"), "visibility")
	repos := r.Body["repos"].([]any)
	eq(t, repos[0].(map[string]any)["cloneUrl"], any("https://github.com/acme/api.git"), "cloneUrl")
	eq(t, repos[0].(map[string]any)["branch"], any("dev"), "branch")
	p := r.Body["prompt"].(map[string]any)
	eq(t, p["type"], any("prompt.submit"), "prompt type")
	eq(t, p["text"], any("fix the flaky checkout test"), "text")
	eq(t, p["mode"], any("plan"), "mode")
	eq(t, p["model"], any("m1"), "model")
	eq(t, p["source"], any("cli"), "source")
}

func TestStartChatHasNoRepo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, _ := h.run("sessions", "start", "--chat", "how do retries work?")
	eq(t, code, 0, "exit code")
	r := h.f.last("POST", "/v1/sessions")
	eq(t, r.Body["stage"], any("chat"), "stage")
	if _, ok := r.Body["repos"]; ok {
		t.Fatal("a chat has no repos")
	}
	code, _, _ = h.run("sessions", "start", "--chat", "--repo", "acme/api", "hi")
	eq(t, code, 2, "--chat with --repo is a usage error")
}

func TestStartRepoFromGitOrigin(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.git = "git@github.com:acme/web.git"
	code, _, errOut := h.run("sessions", "start", "do it")
	eq(t, code, 0, "exit code")
	contains(t, errOut, "Using acme/web from this checkout's git remote.")
	repos := h.f.last("POST", "/v1/sessions").Body["repos"].([]any)
	eq(t, repos[0].(map[string]any)["cloneUrl"], any("https://github.com/acme/web.git"), "cloneUrl")
}

func TestStartNeedsARepo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.git = "https://gitlab.com/acme/web.git"
	code, _, errOut := h.run("sessions", "start", "do it")
	eq(t, code, 2, "exit code")
	contains(t, errOut, "--repo")
	eq(t, h.f.count("POST", "/v1/sessions"), 0, "nothing was created")
}

func TestStartValidation(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	for _, args := range [][]string{
		{"sessions", "start", "x", "--repo", "acme/api", "--mode", "turbo"},
		{"sessions", "start", "x", "--repo", "acme/api", "--visibility", "everyone"},
		{"sessions", "start", "x", "--repo", "acme/api", "--visibility", "team"},
		{"sessions", "start", "x", "--repo", "acme/api", "--team", "t1"},
		{"sessions", "start", "x", "--repo", "not a repo"},
		{"sessions", "start", "  ", "--repo", "acme/api"},
		{"sessions", "start"},
	} {
		code, _, _ := h.run(args...)
		eq(t, code, 2, strings.Join(args, " "))
	}
	eq(t, h.f.count("POST", "/v1/sessions"), 0, "nothing was created")
}

func TestStartPromptFromStdin(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.in = "a long prompt\nover two lines\n"
	code, _, _ := h.run("sessions", "start", "-", "--repo", "acme/api")
	eq(t, code, 0, "exit code")
	eq(t, h.f.last("POST", "/v1/sessions").Body["prompt"].(map[string]any)["text"], any("a long prompt\nover two lines"), "text")
}

func TestStartRetriesWithTheSameIdempotencyKeyOn5xx(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.failCreateWith = []int{503, 502}
	code, out, errOut := h.run("sessions", "start", "x", "--repo", "acme/api")
	eq(t, code, 0, "exit code: "+errOut)
	contains(t, out, "ses_01")
	eq(t, h.f.count("POST", "/v1/sessions"), 3, "two failures, then success")
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	keys := map[string]bool{}
	for _, r := range h.f.reqs {
		if r.Method == "POST" && r.Path == "/v1/sessions" {
			keys[r.Header.Get("Idempotency-Key")] = true
		}
	}
	eq(t, len(keys), 1, "one key reused across every attempt")
	eq(t, h.keys, 1, "the key was generated once")
}

func TestStartRetriesAfterNetworkErrorWithoutDuplicating(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.dropCreate = 2
	code, out, errOut := h.run("sessions", "start", "x", "--repo", "acme/api")
	eq(t, code, 0, "exit code: "+errOut)
	contains(t, out, "ses_01")
	eq(t, h.f.createCount, 1, "one session despite retries")
}

func TestStartGivesUpWithExit7(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.failCreateWith = []int{503, 503, 503, 503, 503, 503}
	code, _, errOut := h.run("sessions", "start", "x", "--repo", "acme/api")
	eq(t, code, 7, "exit code")
	contains(t, errOut, "Error:")
	eq(t, h.f.count("POST", "/v1/sessions"), 4, "first try plus three retries")
}

func TestStartUsesGivenIdempotencyKey(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.run("sessions", "start", "x", "--repo", "acme/api", "--idempotency-key", "my-key-7")
	eq(t, h.f.last("POST", "/v1/sessions").Header.Get("Idempotency-Key"), "my-key-7", "key")
	h.run("sessions", "start", "x", "--repo", "acme/api", "--idempotency-key", "my-key-7")
	eq(t, h.f.createCount, 1, "same key, same session")
}

func completed(seq int, outcome, summary string) map[string]any {
	return map[string]any{"seq": seq, "type": "run.completed", "payload": map[string]any{"runId": "run_1", "outcome": outcome, "summary": summary}}
}

func TestStartWaitSucceeds(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.sessions = []map[string]any{session("ses_01", "Done", "idle", 7, "acme/api", 1)}
	h.f.events["ses_01"] = []map[string]any{{"seq": 1, "type": "prompt.started", "payload": map[string]any{}}, completed(2, "success", "Fixed it")}
	code, out, _ := h.run("sessions", "start", "x", "--repo", "acme/api", "--wait")
	eq(t, code, 0, "exit code")
	contains(t, out, "Run success: Fixed it")
	contains(t, out, "https://github.com/acme/api/pull/7")
}

func TestStartWaitPollsUntilRunEnds(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.events["ses_01"] = []map[string]any{{"seq": 1, "type": "prompt.started", "payload": map[string]any{}}}
	polls := 0
	// The run completes while the CLI sleeps between the third and fourth poll.
	code, out, _ := h.runWithHook(func() {
		polls++
		if polls == 3 {
			h.f.mu.Lock()
			h.f.events["ses_01"] = append(h.f.events["ses_01"], completed(2, "success", "ok"))
			h.f.mu.Unlock()
		}
	}, "--json", "sessions", "start", "x", "--repo", "acme/api", "--wait")
	eq(t, code, 0, "exit code")
	m := mustJSON(t, out)
	eq(t, m["outcome"], any("success"), "outcome")
	eq(t, m["id"], any("ses_01"), "id")
	eq(t, polls, 3, "polled until completion")
	since := h.f.last("GET", "/v1/sessions/ses_01/events").Query["since"][0]
	eq(t, since, "1", "later polls resume after the last seq")
}

func TestStartWaitFailedExits10WithJSONOnStdout(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.events["ses_01"] = []map[string]any{completed(1, "failed", "tests are red")}
	code, out, errOut := h.run("--json", "sessions", "start", "x", "--repo", "acme/api", "--wait")
	eq(t, code, 10, "exit code")
	eq(t, mustJSON(t, out)["outcome"], any("failed"), "stdout still has the result")
	e := mustJSON(t, errOut)["error"].(map[string]any)
	eq(t, e["code"], any("run_failed"), "stderr error code")
	h2 := newHarness(t)
	h2.signIn()
	h2.f.events["ses_01"] = []map[string]any{completed(1, "stopped", "stopped")}
	code, _, _ = h2.run("sessions", "start", "x", "--repo", "acme/api", "--wait")
	eq(t, code, 10, "a stopped run is exit 10 too")
}

func TestStartWaitTimesOutWithExit8(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, errOut := h.run("sessions", "start", "x", "--repo", "acme/api", "--wait", "--wait-timeout", "10s")
	eq(t, code, 8, "exit code")
	contains(t, errOut, "Timed out")
	contains(t, errOut, "/s/ses_01")
}

// ---- stop

func TestStopMany(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, _ := h.run("sessions", "stop", "ses_a", "ses_b", "ses_c", "ses_a")
	eq(t, code, 0, "exit code")
	eq(t, strings.Count(out, "stopped"), 3, "three results, duplicates removed")
	eq(t, h.f.count("POST", "/v1/sessions/ses_a/commands"), 1, "one stop per id")
	r := h.f.last("POST", "/v1/sessions/ses_b/commands")
	eq(t, r.Body["type"], any("run.stop"), "command type")
	if r.Header.Get("Idempotency-Key") == "" {
		t.Fatal("stop sends an idempotency key")
	}
}

func TestSessionsPROpensPullRequestForKeptChanges(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, _ := h.run("sessions", "pr", "ses_a")
	eq(t, code, 0, "exit code")
	contains(t, out, "asked to open a pull request")
	r := h.f.last("POST", "/v1/sessions/ses_a/commands")
	eq(t, r.Body["type"], any("changes.publish"), "command type")
}

func TestStopPartialFailureExits9(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.missing["ses_b"] = true
	code, out, errOut := h.run("--json", "sessions", "stop", "ses_a", "ses_b", "ses_c")
	eq(t, code, 9, "exit code")
	m := mustJSON(t, out)
	eq(t, m["succeeded"], any(float64(2)), "succeeded")
	eq(t, m["failed"], any(float64(1)), "failed")
	results := m["results"].([]any)
	eq(t, results[0].(map[string]any)["id"], any("ses_a"), "results keep the order given")
	eq(t, results[1].(map[string]any)["ok"], any(false), "ses_b failed")
	eq(t, results[1].(map[string]any)["error"].(map[string]any)["code"], any("not_found"), "error code")
	contains(t, errOut, "bulk_partial")
}

func TestStopSingleNotFoundIsExit4(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.missing["ses_x"] = true
	code, _, errOut := h.run("sessions", "stop", "ses_x")
	eq(t, code, 4, "exit code")
	contains(t, errOut, "None of the 1 sessions")
}

func TestStopIDsFromStdin(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.in = "ses_a\nses_b ses_c\n\n"
	code, _, _ := h.run("sessions", "stop", "-")
	eq(t, code, 0, "exit code")
	eq(t, h.f.count("POST", "/v1/sessions/ses_c/commands"), 1, "ids read from stdin")
	h.in = ""
	code, _, _ = h.run("sessions", "stop", "-")
	eq(t, code, 2, "no ids is a usage error")
	code, _, _ = h.run("sessions", "stop")
	eq(t, code, 2, "no args is a usage error")
}

func TestStopRunsFourAtATime(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ids := []string{}
	for i := 0; i < 12; i++ {
		ids = append(ids, fmt.Sprintf("ses_%02d", i))
	}
	code, _, _ := h.run(append([]string{"sessions", "stop"}, ids...)...)
	eq(t, code, 0, "exit code")
	for _, id := range ids {
		eq(t, h.f.count("POST", "/v1/sessions/"+id+"/commands"), 1, id)
	}
}

// ---- share

func TestShareLinkInviteRemoveVisibility(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya", "role": "owner"}, {"id": "usr_7", "login": "Sam", "role": "viewer"}}
	code, out, errOut := h.run("sessions", "share", "ses_01", "--link", "viewer", "--invite", "@alex", "--invite", "jo", "--role", "collaborator",
		"--remove", "sam", "--visibility", "workspace")
	eq(t, code, 0, "exit code: "+errOut)
	contains(t, out, "Share link (viewer): https://app.waniis.com/join/tok123")
	contains(t, out, "Invited @alex as collaborator")
	contains(t, out, "Removed sam")
	contains(t, out, "Visibility set to workspace")
	eq(t, h.f.last("PUT", "/v1/sessions/ses_01/share-link").Body["role"], any("viewer"), "link role")
	inv := h.f.last("POST", "/v1/sessions/ses_01/people").Body
	eq(t, inv["login"], any("jo"), "last invited login")
	eq(t, inv["role"], any("collaborator"), "role")
	eq(t, h.f.count("POST", "/v1/sessions/ses_01/people"), 2, "two invites, the @ stripped")
	eq(t, h.f.count("DELETE", "/v1/sessions/ses_01/people/usr_7"), 1, "removed by user id found from login")
	eq(t, h.f.last("PUT", "/v1/sessions/ses_01/visibility").Body["visibility"], any("workspace"), "visibility")
}

func TestShareJSONAndRevoke(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, _ := h.run("--json", "sessions", "share", "ses_01", "--link", "collaborator")
	eq(t, code, 0, "exit code")
	m := mustJSON(t, out)
	eq(t, m["link"].(map[string]any)["url"], any("https://app.waniis.com/join/tok123"), "link url")
	code, out, _ = h.run("sessions", "share", "ses_01", "--revoke-link")
	eq(t, code, 0, "revoke exit code")
	contains(t, out, "revoked")
	eq(t, h.f.count("DELETE", "/v1/sessions/ses_01/share-link"), 1, "revoked")
}

func TestShareUsageAndErrors(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	for _, args := range [][]string{
		{"sessions", "share", "ses_01"},
		{"sessions", "share", "ses_01", "--link", "owner"},
		{"sessions", "share", "ses_01", "--link", "viewer", "--revoke-link"},
		{"sessions", "share", "ses_01", "--visibility", "team"},
		{"sessions", "share", "ses_01", "--invite", "x", "--role", "admin"},
	} {
		code, _, _ := h.run(args...)
		eq(t, code, 2, strings.Join(args, " "))
	}
	h.f.people = []map[string]any{{"id": "usr_1", "login": "maya"}}
	code, _, errOut := h.run("sessions", "share", "ses_01", "--remove", "ghost")
	eq(t, code, 4, "removing someone who isn't there")
	contains(t, errOut, "ghost")
}

// ---- open

func TestOpen(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("sessions", "open", "ses_01")
	eq(t, code, 0, "exit code")
	eq(t, strings.TrimSpace(out), h.f.srv.URL+"/s/ses_01", "URL printed")
	eq(t, h.opened[0], h.f.srv.URL+"/s/ses_01", "browser opened")
	code, out, _ = h.run("--json", "sessions", "open", "ses_01")
	eq(t, code, 0, "json exit code")
	eq(t, mustJSON(t, out)["opened"], any(true), "opened")
}

func TestOpenMapsAPIAliasToApp(t *testing.T) {
	h := newHarness(t)
	var out string
	_, out, _ = h.run("--api-url", "https://api.waniis.com", "sessions", "open", "ses_01")
	eq(t, strings.TrimSpace(out), "https://app.waniis.com/s/ses_01", "api.waniis.com links to app.waniis.com")
}

func TestSessionsAlias(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, _ := h.run("s", "ls")
	eq(t, code, 0, "alias exit code")
}
