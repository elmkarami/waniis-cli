package commands

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/elmkarami/waniis-cli/internal/api"
)

func TestErrorMappingToExitCodes(t *testing.T) {
	cases := []struct {
		status int
		body   string
		exit   int
		msg    string
	}{
		{401, `{"error":"unauthorized"}`, 3, "waniis login"},
		{403, `{"error":"access_pending","message":"x"}`, 3, "hasn't been approved yet"},
		{403, `{"error":"insufficient_scope"}`, 3, "scope"},
		{403, `{"error":"forbidden","message":"Only the session's owner can do that."}`, 4, "Only the session's owner"},
		{403, `{"error":"reason_required"}`, 4, "reason is required"},
		{404, `{"error":"not_found"}`, 4, "Not found"},
		{410, `{"error":"session_deleted"}`, 4, "deleted"},
		{409, `{"error":"invalid_state","message":"A plan is already approved."}`, 5, "already approved"},
		{402, `{"error":"billing_blocked","message":"Billing is blocked."}`, 6, "Billing"},
		{429, `{"error":"rate_limited"}`, 7, "Too many"},
		{503, `{"error":"session_list_unavailable"}`, 7, "session_list_unavailable"},
		{400, `{"error":"invalid_body","issues":[{"path":["repos"],"message":"Choose at least one repository"}]}`, 1, "repos Choose at least one repository"},
		{500, `not json`, 1, "HTTP 500"},
	}
	for _, c := range cases {
		e := api.MapError(c.status, []byte(c.body), "req_1")
		eq(t, e.Code, c.exit, c.body)
		if !strings.Contains(e.Message+" "+e.Hint+" "+e.APICode, c.msg) {
			t.Errorf("%s: want %q in message=%q hint=%q code=%q", c.body, c.msg, e.Message, e.Hint, e.APICode)
		}
		eq(t, e.RequestID, "req_1", "request id")
	}
}

func TestAccessPendingEndToEnd(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.accessPending = true
	code, _, errOut := h.run("sessions", "list")
	eq(t, code, 3, "exit code")
	contains(t, errOut, "Your access request hasn't been approved yet.")
	contains(t, errOut, "request id: req_test")
}

func TestJSONErrorShape(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.f.accessPending = true
	code, out, errOut := h.run("--json", "sessions", "list")
	eq(t, code, 3, "exit code")
	eq(t, out, "", "stdout stays clean on error")
	e := mustJSON(t, errOut)["error"].(map[string]any)
	eq(t, e["code"], any("access_pending"), "code")
	eq(t, e["status"], any(float64(403)), "status")
	eq(t, e["requestId"], any("req_test"), "requestId")
	if e["message"] == "" || e["hint"] == "" {
		t.Fatalf("message and hint expected: %v", e)
	}
}

func TestUsageErrorsAreExit2(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"bogus"}, {"sessions", "list", "--limit", "x"}, {"sessions", "open"}, {"--api-url", "ftp://x", "whoami"}} {
		h.signIn()
		code, _, errOut := h.run(args...)
		eq(t, code, 2, strings.Join(args, " "))
		contains(t, errOut, "Error:")
	}
	code, _, errOut := h.run("--json", "bogus")
	eq(t, code, 2, "json usage error")
	eq(t, mustJSON(t, errOut)["error"].(map[string]any)["code"], any("usage"), "usage code")
}

func TestAPIURLMustBeHTTPSExceptLocalhost(t *testing.T) {
	var out, errb bytes.Buffer
	d := DefaultDeps(nil)
	d.Out, d.Err = &out, &errb
	d.Getenv = func(string) string { return "" }
	code := Execute(context.Background(), d, []string{"--api-url", "http://example.com", "whoami"})
	eq(t, code, 2, "http to a remote host is refused")
	contains(t, errb.String(), "https")
}

func TestAPIURLFromEnv(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.env["WANIIS_API_URL"] = h.f.srv.URL
	var out, errb bytes.Buffer
	d := DefaultDeps(h.store)
	d.Out, d.Err, d.Getenv, d.HTTP = &out, &errb, func(k string) string { return h.env[k] }, h.f.srv.Client()
	code := Execute(context.Background(), d, []string{"whoami"})
	eq(t, code, 0, "env URL is used: "+errb.String())
}

func TestWorkspaceFromEnvAndFlagWins(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.env["WANIIS_WORKSPACE"] = "ws_env"
	h.run("whoami")
	eq(t, h.f.last("GET", "/v1/me").Header.Get("x-rafiiq-workspace"), "ws_env", "env workspace")
	h.run("--workspace", "ws_flag", "whoami")
	eq(t, h.f.last("GET", "/v1/me").Header.Get("x-rafiiq-workspace"), "ws_flag", "flag wins")
}

func TestVerboseLogsNeverShowTokens(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, errOut := h.run("--verbose", "whoami")
	eq(t, code, 0, "exit code")
	contains(t, errOut, "> GET")
	contains(t, errOut, "< 200")
	notContains(t, errOut, testPAT)
	notContains(t, out, testPAT)
}

func TestNoTokenInAnyOutput(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	seed(h, 2)
	all := ""
	for _, args := range [][]string{
		{"whoami"}, {"--json", "whoami"}, {"sessions", "list"}, {"--json", "sessions", "list", "--verbose"},
		{"sessions", "show", "ses_001"}, {"sessions", "start", "x", "--repo", "acme/api", "--verbose"},
		{"sessions", "stop", "a", "--verbose"}, {"sessions", "search", "x", "--verbose"},
		{"sessions", "share", "ses_001", "--link", "viewer", "--verbose"}, {"export", "sessions", "--verbose"},
		{"--verbose", "login", "--token", testPAT},
	} {
		_, o, e := h.run(args...)
		all += o + e
	}
	for _, tok := range []string{testPAT, "rafiiq_pat_", "rafiiq_cli_"} {
		notContains(t, all, tok)
	}
}

func TestVersionAndCompletion(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("version")
	eq(t, code, 0, "exit code")
	contains(t, out, "waniis dev")
	code, out, _ = h.run("--json", "version")
	eq(t, code, 0, "json exit code")
	eq(t, mustJSON(t, out)["version"], any("dev"), "version")
	for shell, marker := range map[string]string{"bash": "bash completion", "zsh": "#compdef", "fish": "complete -c waniis", "powershell": "Register-ArgumentCompleter"} {
		code, out, errOut := h.run("completion", shell)
		eq(t, code, 0, shell+" exit code: "+errOut)
		contains(t, out, marker)
	}
}

func TestHelpMentionsWaniisNotRafiiq(t *testing.T) {
	h := newHarness(t)
	_, out, _ := h.run("--help")
	contains(t, out, "waniis")
	notContains(t, strings.ToLower(out), "rafiiq")
}
