package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

// PromptCommand is a `prompt.submit` command.
type PromptCommand struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Mode   string `json:"mode,omitempty"`
	Model  string `json:"model,omitempty"`
	Source string `json:"source"`
}

// CommandResult is the answer to POST /v1/sessions/:id/commands. A prompt.submit answers with the
// queued prompt (promptId, position, and runId when it started at once) and a run.steer with the
// run it steered; the events the command produced are kept too, so the ids are also found there
// when the server only sends events.
type CommandResult struct {
	Events   []Event `json:"events"`
	PromptID string  `json:"promptId"`
	RunID    string  `json:"runId"`
	Position *int    `json:"position"`
	Queued   *bool   `json:"queued"`
}

// Resolve fills PromptID, RunID and Position from the events when the server didn't send them.
func (r *CommandResult) Resolve() {
	for _, e := range r.Events {
		var p struct {
			PromptID string `json:"promptId"`
			RunID    string `json:"runId"`
			Position *int   `json:"position"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		switch e.Type {
		case "prompt.queued":
			if r.PromptID == "" {
				r.PromptID = p.PromptID
			}
			if r.Position == nil {
				r.Position = p.Position
			}
		case "prompt.started", "run.steered":
			if r.RunID == "" {
				r.RunID = p.RunID
			}
		}
	}
}

// Command is POST /v1/sessions/:id/commands with an Idempotency-Key (key empty: a fresh one).
func (c *Client) Command(ctx context.Context, id string, body any, key string) (*CommandResult, error) {
	var out CommandResult
	_, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/sessions/" + url.PathEscape(id) + "/commands",
		Body: body, Idempotent: true, Key: key}, &out)
	if err != nil {
		return nil, err
	}
	out.Resolve()
	return &out, nil
}

// SubmitPrompt appends a prompt: queued when a run is going, started when the session is idle.
func (c *Client) SubmitPrompt(ctx context.Context, id string, p PromptCommand, key string) (*CommandResult, error) {
	p.Type, p.Source = "prompt.submit", "cli"
	return c.Command(ctx, id, p, key)
}

// Steer steers the running run.
func (c *Client) Steer(ctx context.Context, id, text, key string) (*CommandResult, error) {
	return c.Command(ctx, id, map[string]string{"type": "run.steer", "text": text}, key)
}

// RunStatus is the current run in a Status.
type RunStatus struct {
	ID        string `json:"id"`
	PromptID  string `json:"promptId,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Stage     string `json:"stage,omitempty"` // starting, working, verifying, publishing
	StartedAt string `json:"startedAt,omitempty"`
}

// OutcomeStatus is how the last run ended.
type OutcomeStatus struct {
	ID      string `json:"id,omitempty"`
	Outcome string `json:"outcome"`
	Summary string `json:"summary,omitempty"`
	At      string `json:"at,omitempty"`
}

// PRStatus is a session's pull request.
type PRStatus struct {
	Repo   string `json:"repo,omitempty"`
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
	State  string `json:"state,omitempty"` // only in what the CLI derives itself
}

// KeptStatus is what a run changed but didn't publish. Ready: the run succeeded and the changes
// wait for someone to open a pull request.
type KeptStatus struct {
	RunID     string `json:"runId"`
	Files     int    `json:"files"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Ready     bool   `json:"ready"`
}

// SandboxStatus is the sandbox's state.
type SandboxStatus struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ApprovalStatus is something waiting for a person (derived from the events by the CLI).
type ApprovalStatus struct {
	Kind      string `json:"kind"` // tool or network
	RequestID string `json:"requestId"`
	What      string `json:"what"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// Status is GET /v1/sessions/:id/status:
// {stage, activity, run, queued, lastRun, pullRequest, sandbox, lastSeq, at}. When the server
// has no such route the CLI derives the same shape from the session's latest events.
type Status struct {
	Stage       string         `json:"stage"`    // chat or working
	Activity    string         `json:"activity"` // running or idle
	Run         *RunStatus     `json:"run"`
	Queued      int            `json:"queued"`
	LastRun     *OutcomeStatus `json:"lastRun"`
	PullRequest *PRStatus      `json:"pullRequest"`
	KeptChanges *KeptStatus    `json:"keptChanges"`
	Sandbox     *SandboxStatus `json:"sandbox"`
	LastSeq     int            `json:"lastSeq"`
	At          string         `json:"at,omitempty"`
}

// GetStatus is GET /v1/sessions/:id/status; the raw JSON is returned too, so --json passes
// every field through.
func (c *Client) GetStatus(ctx context.Context, id string) (*Status, json.RawMessage, error) {
	var raw json.RawMessage
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/" + url.PathEscape(id) + "/status"}, &raw); err != nil {
		return nil, nil, err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, nil, err
	}
	return &st, raw, nil
}

// NetworkDecision answers an agent's request for a host.
func (c *Client) NetworkDecide(ctx context.Context, id, requestID string, approve bool, scope string) error {
	body := map[string]any{"kind": "decide", "requestId": requestID, "decision": "deny"}
	if approve {
		body["decision"] = "approve"
		if scope != "" {
			body["scope"] = scope
		}
	}
	_, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/sessions/" + url.PathEscape(id) + "/network/access", Body: body}, nil)
	return err
}

// NetworkAllow allows a host the sandbox was refused (workspace owners and admins only).
func (c *Client) NetworkAllow(ctx context.Context, id, host, scope, access string) error {
	body := map[string]any{"kind": "allow", "host": host, "scope": scope, "access": access, "retry": true}
	_, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/sessions/" + url.PathEscape(id) + "/network/access", Body: body}, nil)
	return err
}

// ToolApprovalDecide is the `tool.approval.decide` command: once, session or deny.
func (c *Client) ToolApprovalDecide(ctx context.Context, id, requestID, decision string) error {
	_, err := c.Command(ctx, id, map[string]string{"type": "tool.approval.decide", "requestId": requestID, "decision": decision, "source": "cli"}, "")
	return err
}

// StartWorking accepts "Work on this in a sandbox?" (the `session.start_working` command).
func (c *Client) StartWorking(ctx context.Context, id, proposalID string) error {
	body := map[string]string{"type": "session.start_working", "source": "cli"}
	if proposalID != "" {
		body["proposalId"] = proposalID
	}
	_, err := c.Command(ctx, id, body, "")
	return err
}

// Surface is a preview port of a session's sandbox.
type Surface struct {
	Port      int    `json:"port"`
	Label     string `json:"label"`
	Service   string `json:"service"`
	Listening bool   `json:"listening"`
}

// SurfacesView is GET /v1/sessions/:id/surfaces.
type SurfacesView struct {
	Sandbox  string    `json:"sandbox"`
	Previews bool      `json:"previews"`
	Ports    []Surface `json:"ports"`
}

// Surfaces is GET /v1/sessions/:id/surfaces.
func (c *Client) Surfaces(ctx context.Context, id string) (*SurfacesView, error) {
	var v SurfacesView
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/" + url.PathEscape(id) + "/surfaces"}, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// PreviewURL asks for a surface's one-time link (GET /:id/surfaces/:slug/open answers a 302 with
// a 60-second, single-use ticket) and returns the Location without following it.
func (c *Client) PreviewURL(ctx context.Context, id, slug string) (string, error) {
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.BaseURL+"/v1/sessions/"+url.PathEscape(id)+"/surfaces/"+url.PathEscape(slug)+"/open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if c.Workspace != "" {
		req.Header.Set(HeaderWorkspace, c.Workspace)
	}
	if c.Tokens != nil {
		tok, err := c.Tokens.Token(ctx)
		if err != nil {
			return "", err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	c.logf("> GET %s", Redact(req.URL.String()))
	res, err := hc.Do(req)
	if err != nil {
		return "", networkError(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return res.Header.Get("Location"), nil
	}
	return "", MapError(res.StatusCode, body, res.Header.Get("x-request-id"))
}

// DiffFile is one changed file in a session's diff.
type DiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// RepoDiff is one repo's changes: pushed (from GitHub) or kept (still in the sandbox).
type RepoDiff struct {
	Repo        string `json:"repo"`
	Source      string `json:"source"`
	PullRequest *struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	} `json:"pullRequest"`
	Files      []DiffFile `json:"files"`
	TotalFiles int        `json:"totalFiles"`
}

// SessionDiff is GET /v1/sessions/:id/diff, without the patches.
type SessionDiff struct {
	Repos []RepoDiff `json:"repos"`
	Gaps  []struct {
		Source string `json:"source"`
		Repo   string `json:"repo"`
		Reason string `json:"reason"`
	} `json:"gaps"`
}

// Diff is GET /v1/sessions/:id/diff.
func (c *Client) Diff(ctx context.Context, id string) (*SessionDiff, error) {
	var d SessionDiff
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/" + url.PathEscape(id) + "/diff"}, &d); err != nil {
		return nil, err
	}
	return &d, nil
}
