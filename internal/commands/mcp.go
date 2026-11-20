package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/render"
	"github.com/elmkarami/waniis-cli/internal/sessionstate"
)

func (a *app) mcpCmd() *cobra.Command {
	var readOnly bool
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server over stdio, for AI agents (Claude Code, Codex, Cursor)",
		Long: `Run a Model Context Protocol server on stdin and stdout, with your stored credentials, so an
AI agent can start, watch, prompt, steer, stop and wait for Waniis sessions.

Tools: list_sessions, search_sessions, start_session, session_status, session_logs,
prompt_session, steer_session, stop_session, wait_session. --read-only leaves out the ones that
change anything (start, prompt, steer, stop).

Add it to Claude Code with:  claude mcp add waniis -- waniis mcp`,
		Example: "  claude mcp add waniis -- waniis mcp\n  waniis mcp --read-only",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			// Stdout belongs to the protocol: nothing else may write there.
			err = a.mcpServer(rt, readOnly).Run(cmd.Context(), &mcp.StdioTransport{})
			if err != nil && cmd.Context().Err() == nil {
				return err
			}
			return nil
		},
	}
	c.Flags().BoolVar(&readOnly, "read-only", false, "offer only the tools that don't change anything")
	return c
}

// ---- tool inputs and outputs ----

type sessionBrief struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Status       string           `json:"status"`
	Stage        string           `json:"stage,omitempty"`
	Repos        []string         `json:"repos"`
	PullRequest  *api.PullRequest `json:"pullRequest,omitempty"`
	LastActiveAt string           `json:"lastActiveAt,omitempty"`
	URL          string           `json:"url"`
}

type listInput struct {
	Status []string `json:"status,omitempty" jsonschema:"only these statuses: running, idle, sleeping, failed"`
	Repo   []string `json:"repo,omitempty" jsonschema:"only these repos (owner/name)"`
	Author string   `json:"author,omitempty" jsonschema:"me, or a login"`
	HasPR  bool     `json:"hasPr,omitempty" jsonschema:"only sessions that opened a pull request"`
	Since  string   `json:"since,omitempty" jsonschema:"active since this ISO 8601 date or time"`
	Limit  int      `json:"limit,omitempty" jsonschema:"how many to return, 1 to 100 (default 20)"`
}

type listOutput struct {
	Items []sessionBrief `json:"items"`
	Next  string         `json:"next,omitempty"`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"what to look for, in plain language"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many to return, 1 to 100 (default 20)"`
}

type searchOutput struct {
	Items       []sessionBrief `json:"items"`
	Interpreted string         `json:"interpreted,omitempty"`
}

type startInput struct {
	Prompt         string `json:"prompt" jsonschema:"what to do"`
	Repo           string `json:"repo,omitempty" jsonschema:"repo to work on, owner/name or a GitHub URL; defaults to the git origin of the server's directory"`
	Chat           bool   `json:"chat,omitempty" jsonschema:"a conversation with no repo and no sandbox"`
	Mode           string `json:"mode,omitempty" jsonschema:"auto, build, plan or ask (ask is read-only)"`
	Model          string `json:"model,omitempty" jsonschema:"model for the first run"`
	Wait           bool   `json:"wait,omitempty" jsonschema:"wait for the first run to end"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"with wait: give up after this many seconds (default 300); the session keeps running"`
}

type runResult struct {
	ID           string         `json:"id"`
	URL          string         `json:"url"`
	PromptID     string         `json:"promptId,omitempty"`
	Queued       bool           `json:"queued,omitempty"`
	Position     int            `json:"position,omitempty"`
	Waited       bool           `json:"waited,omitempty"`
	TimedOut     bool           `json:"timedOut,omitempty"`
	Outcome      string         `json:"outcome,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	PullRequests []api.PRStatus `json:"pullRequests,omitempty"`
	Status       *api.Status    `json:"status,omitempty"`
	Steered      bool           `json:"steered,omitempty"`
	RunID        string         `json:"runId,omitempty"`
	Stopped      bool           `json:"stopped,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type idInput struct {
	ID string `json:"id" jsonschema:"the session id, e.g. ses_01abc"`
}

type statusOutput struct {
	ID string `json:"id"`
	api.Status
	URL string `json:"url"`
}

type logsInput struct {
	ID     string `json:"id" jsonschema:"the session id"`
	Tail   int    `json:"tail,omitempty" jsonschema:"the last N events (default 60, up to 1000)"`
	Since  int    `json:"since,omitempty" jsonschema:"only events after this sequence number (a previous lastSeq)"`
	Format string `json:"format,omitempty" jsonschema:"text (readable lines, default) or events (the raw event objects)"`
}

type logsOutput struct {
	Text    string `json:"text,omitempty"`
	Events  []any  `json:"events,omitempty"`
	Count   int    `json:"count"`
	LastSeq int    `json:"lastSeq"`
	HasMore bool   `json:"hasMore,omitempty"`
}

type promptInput struct {
	ID             string `json:"id" jsonschema:"the session id"`
	Text           string `json:"text" jsonschema:"the prompt: queued if a run is going, starts a run if the session is idle"`
	Mode           string `json:"mode,omitempty" jsonschema:"auto, build, plan or ask"`
	Model          string `json:"model,omitempty"`
	Wait           bool   `json:"wait,omitempty" jsonschema:"wait for this prompt's run to end"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"with wait: give up after this many seconds (default 300)"`
}

type steerInput struct {
	ID   string `json:"id" jsonschema:"the session id"`
	Text string `json:"text" jsonschema:"what to tell the running run; it reads it at its next step"`
}

type waitInput struct {
	ID             string `json:"id" jsonschema:"the session id"`
	Until          string `json:"until,omitempty" jsonschema:"run-end (default), idle or pr-open"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"give up after this many seconds (default 300); call again to keep waiting"`
}

// ---- the server ----

func briefOf(base string, s api.Session) sessionBrief {
	b := sessionBrief{ID: s.ID, Title: s.Title, Status: s.Status, Stage: s.Stage, PullRequest: s.PullRequest, URL: sessionURL(base, s.ID), Repos: []string{}}
	for _, r := range s.Repos {
		b.Repos = append(b.Repos, r.FullName)
	}
	if !s.LastActiveAt.IsZero() {
		b.LastActiveAt = s.LastActiveAt.UTC().Format(time.RFC3339)
	}
	return b
}

// toolError reports a failure to the agent as a tool error, with the CLI's exit code and hint.
func toolError(err error) *mcp.CallToolResult {
	ee := exitcode.As(err)
	msg := ee.Message
	if ee.Hint != "" {
		msg += " " + ee.Hint
	}
	if ee.RequestID != "" {
		msg += " (request " + ee.RequestID + ")"
	}
	msg += fmt.Sprintf(" [exit code %d]", ee.Code)
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func limitOf(n int) int {
	switch {
	case n <= 0:
		return 20
	case n > 100:
		return 100
	}
	return n
}

func secs(n int) time.Duration {
	if n <= 0 {
		n = 300
	}
	return min(time.Duration(n), 3600) * time.Second
}

// mcpServer builds the server. Credentials are read when a tool is called, so a missing sign-in
// is a tool error the agent can read, not a failure to start.
func (a *app) mcpServer(rt *conn, readOnly bool) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "waniis", Version: Version}, nil)
	read := &mcp.ToolAnnotations{ReadOnlyHint: true}
	write := &mcp.ToolAnnotations{}
	stop := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}

	mcp.AddTool(srv, &mcp.Tool{Name: "list_sessions", Annotations: read,
		Description: "List Waniis sessions you can see, newest activity first. Filter by status, repo, author, pull request."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, listOutput, error) {
			f := api.ListFilter{Status: in.Status, Repo: in.Repo, Author: in.Author, HasPR: in.HasPR, Since: in.Since, Limit: limitOf(in.Limit)}
			page, err := rt.Client.ListSessions(ctx, f)
			if err != nil {
				return toolError(err), listOutput{}, nil
			}
			out := listOutput{Items: []sessionBrief{}, Next: page.Cursor()}
			for _, s := range page.All() {
				out.Items = append(out.Items, briefOf(rt.Base, s))
			}
			return nil, out, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "search_sessions", Annotations: read,
		Description: "Search sessions in plain language, e.g. \"payment bugs fixed last week that have a merged PR\"."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
			if strings.TrimSpace(in.Query) == "" {
				return toolError(exitcode.Usagef("query is empty")), searchOutput{}, nil
			}
			page, err := rt.Client.SearchSessions(ctx, api.ListFilter{Query: strings.TrimSpace(in.Query), Limit: limitOf(in.Limit)})
			if err != nil {
				return toolError(err), searchOutput{}, nil
			}
			out := searchOutput{Items: []sessionBrief{}}
			if page.Interpreted != nil {
				out.Interpreted = interpretationLine(page.Interpreted)
			}
			for _, s := range page.All() {
				out.Items = append(out.Items, briefOf(rt.Base, s))
			}
			return nil, out, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "session_status", Annotations: read,
		Description: "One call: is the session running or idle, the current run and its stage, queued prompts, how the last run ended, the pull request and the sandbox."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, statusOutput, error) {
			st, _, err := a.fetchStatus(ctx, rt, in.ID)
			if err != nil {
				return toolError(err), statusOutput{}, nil
			}
			return nil, statusOutput{ID: in.ID, Status: *st, URL: sessionURL(rt.Base, in.ID)}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "session_logs", Annotations: read,
		Description: "Read what a session did: the last N events (default 60) as readable lines, or the events after a sequence number you saw before (use lastSeq to continue)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, logsOutput, error) {
			out, err := a.mcpLogs(ctx, rt, in)
			if err != nil {
				return toolError(err), logsOutput{}, nil
			}
			return nil, *out, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "wait_session", Annotations: read,
		Description: "Block until the session's current run ends (run-end, the default), nothing is running or queued (idle) or a pull request is open (pr-open). Returns the outcome (success, failed, stopped, needs_input), the summary and pull requests; timedOut is true if it didn't happen in time, and you can call it again."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in waitInput) (*mcp.CallToolResult, runResult, error) {
			u := until(in.Until)
			if u == "" {
				u = untilRunEnd
			}
			if _, err := parseUntil(string(u)); err != nil {
				return toolError(err), runResult{}, nil
			}
			res, err := a.mcpWait(ctx, rt, in.ID, waitSpec{Until: u, Timeout: secs(in.TimeoutSeconds)})
			if err != nil {
				return toolError(err), runResult{}, nil
			}
			return nil, *res, nil
		})

	if readOnly {
		return srv
	}

	mcp.AddTool(srv, &mcp.Tool{Name: "start_session", Annotations: write,
		Description: "Start a Waniis session with a first prompt, on a repo (or chat: true for a conversation with no repo). Returns the session id and URL; with wait: true, also how the first run ended."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in startInput) (*mcp.CallToolResult, runResult, error) {
			res, err := a.mcpStart(ctx, rt, in)
			if err != nil {
				return toolError(err), runResult{}, nil
			}
			return nil, *res, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "prompt_session", Annotations: write,
		Description: "Add a prompt to a session: queued behind a running run, or starts one if the session is idle. Use steer_session instead to redirect the run that is going."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in promptInput) (*mcp.CallToolResult, runResult, error) {
			res, err := a.mcpPrompt(ctx, rt, in)
			if err != nil {
				return toolError(err), runResult{}, nil
			}
			return nil, *res, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "steer_session", Annotations: write,
		Description: "Steer the run that is going: the agent reads the text at its next step. Fails when nothing is running (use prompt_session then)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in steerInput) (*mcp.CallToolResult, runResult, error) {
			if strings.TrimSpace(in.Text) == "" {
				return toolError(exitcode.Usagef("text is empty")), runResult{}, nil
			}
			if st, _, err := a.fetchStatus(ctx, rt, in.ID); err == nil && st.Activity == "idle" {
				return toolError(steerError(nil, in.ID)), runResult{}, nil
			}
			res, err := rt.Client.Steer(ctx, in.ID, strings.TrimSpace(in.Text), a.NewKey())
			if err != nil {
				return toolError(steerError(err, in.ID)), runResult{}, nil
			}
			return nil, runResult{ID: in.ID, URL: sessionURL(rt.Base, in.ID), Steered: true, RunID: res.RunID}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "stop_session", Annotations: stop,
		Description: "Stop the session's running work. What the run changed is kept."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, runResult, error) {
			if err := rt.Client.StopRun(ctx, in.ID); err != nil {
				return toolError(err), runResult{}, nil
			}
			return nil, runResult{ID: in.ID, URL: sessionURL(rt.Base, in.ID), Stopped: true}, nil
		})
	return srv
}

func boolPtr(b bool) *bool { return &b }

func (a *app) mcpLogs(ctx context.Context, rt *conn, in logsInput) (*logsOutput, error) {
	var evs []api.Event
	var err error
	switch {
	case in.Since > 0:
		var page *api.EventsPage
		if page, err = rt.Client.EventsPage(ctx, in.ID, api.EventsQuery{Since: in.Since, Limit: 1000}); err != nil {
			return nil, err
		}
		evs = page.Events
		out := &logsOutput{Count: len(evs), LastSeq: page.LastSeq, HasMore: page.HasMore}
		return a.fillLogs(ctx, rt, in, out, evs)
	default:
		n := in.Tail
		if n <= 0 {
			n = 60
		}
		if n > 1000 {
			n = 1000
		}
		page, err := rt.Client.EventsPage(ctx, in.ID, api.EventsQuery{Tail: n})
		if err != nil {
			return nil, err
		}
		evs = page.Events
		if len(evs) > n {
			evs = evs[len(evs)-n:]
		}
		out := &logsOutput{Count: len(evs), LastSeq: page.LastSeq, HasMore: page.HasMore}
		return a.fillLogs(ctx, rt, in, out, evs)
	}
}

func (a *app) fillLogs(ctx context.Context, rt *conn, in logsInput, out *logsOutput, evs []api.Event) (*logsOutput, error) {
	if len(evs) > 0 && evs[len(evs)-1].Seq > out.LastSeq {
		out.LastSeq = evs[len(evs)-1].Seq
	}
	switch in.Format {
	case "", "text":
		var b strings.Builder
		r := render.New(&b, render.Options{Width: 100, People: a.people(ctx, rt, in.ID)})
		for _, ev := range evs {
			r.Event(ev)
		}
		r.Flush()
		out.Text = b.String()
	case "events":
		out.Events = make([]any, 0, len(evs))
		for _, ev := range evs {
			var m map[string]any
			if b, err := ev.MarshalJSON(); err == nil && json.Unmarshal(b, &m) == nil {
				out.Events = append(out.Events, m)
			}
		}
	default:
		return nil, exitcode.Usagef("format must be text or events")
	}
	return out, nil
}

// mcpWait is waitFor with the result shaped for an agent: a timeout is a result, not an error.
func (a *app) mcpWait(ctx context.Context, rt *conn, id string, w waitSpec) (*runResult, error) {
	st, err := a.waitFor(ctx, rt, id, w)
	res := &runResult{ID: id, URL: sessionURL(rt.Base, id), Waited: true}
	if err != nil {
		switch exitcode.Of(err) {
		case exitcode.Timeout:
			res.TimedOut = true
			err = nil
		case exitcode.RunFailed:
			err = nil
		default:
			if exitcode.As(err).APICode == "no_pull_request" {
				err = nil
				res.Summary = "The session went idle without opening a pull request."
			}
		}
		if err != nil {
			return nil, err
		}
	}
	fillRun(res, st)
	return res, nil
}

func fillRun(res *runResult, st *sessionstate.State) {
	if st == nil {
		return
	}
	if st.Last != nil && !res.TimedOut {
		res.Outcome = st.Last.Outcome
		if res.Summary == "" {
			res.Summary = st.Last.Summary
		}
	}
	res.PullRequests = st.PRs
	res.Status = st.Status()
}

func (a *app) mcpStart(ctx context.Context, rt *conn, in startInput) (*runResult, error) {
	text := strings.TrimSpace(in.Prompt)
	if text == "" {
		return nil, exitcode.Usagef("prompt is empty")
	}
	if err := firstErr(oneOf("mode", in.Mode, validModes)); err != nil {
		return nil, err
	}
	body := api.CreateSessionInput{Prompt: &api.PromptInput{Type: "prompt.submit", Text: text, Mode: in.Mode, Model: in.Model, Source: "cli"}}
	if in.Chat {
		body.Stage = "chat"
	} else {
		var flags []string
		if in.Repo != "" {
			flags = []string{in.Repo}
		}
		names, err := a.resolveRepos(flags)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			body.Repos = append(body.Repos, api.RepoInput{CloneURL: cloneURL(n)})
		}
	}
	created, err := rt.Client.CreateSession(ctx, body, a.NewKey())
	if err != nil {
		return nil, err
	}
	res := &runResult{ID: created.ID, URL: sessionURL(rt.Base, created.ID)}
	if !in.Wait {
		return res, nil
	}
	return a.mcpWait(ctx, rt, created.ID, waitSpec{Until: untilRunEnd, ExpectRun: true, Timeout: secs(in.TimeoutSeconds)})
}

func (a *app) mcpPrompt(ctx context.Context, rt *conn, in promptInput) (*runResult, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, exitcode.Usagef("text is empty")
	}
	if err := oneOf("mode", in.Mode, validModes); err != nil {
		return nil, err
	}
	cmd, err := rt.Client.SubmitPrompt(ctx, in.ID, api.PromptCommand{Text: text, Mode: in.Mode, Model: in.Model}, a.NewKey())
	if err != nil {
		return nil, err
	}
	res := &runResult{ID: in.ID, URL: sessionURL(rt.Base, in.ID), PromptID: cmd.PromptID, RunID: cmd.RunID}
	if cmd.Position != nil && *cmd.Position > 0 {
		res.Queued, res.Position = true, *cmd.Position
	}
	if !in.Wait {
		return res, nil
	}
	w, err := a.mcpWait(ctx, rt, in.ID, waitSpec{Until: untilRunEnd, PromptID: cmd.PromptID, ExpectRun: true, Timeout: secs(in.TimeoutSeconds)})
	if err != nil {
		return nil, err
	}
	w.PromptID, w.Queued, w.Position, w.RunID = res.PromptID, res.Queued, res.Position, res.RunID
	return w, nil
}
