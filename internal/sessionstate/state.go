// Package sessionstate folds a session's events into what a person asks about: is a run going,
// what is queued, how did the last one end, which pull requests exist, what waits for a person.
// It is pure; the commands feed it events from the API or the stream.
package sessionstate

import (
	"encoding/json"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/api"
)

// Run is the run in progress.
type Run struct {
	ID    string
	Mode  string
	Stage string // starting, working, verifying, reviewing, waiting
}

// Prompt is a queued prompt.
type Prompt struct {
	ID, Author, Text, Mode string
}

// State is the fold of the events applied so far.
type State struct {
	LastSeq     int
	Stage       string // chat or working, from session.created / session.working
	Sandbox     string
	Active      *Run
	Queued      []Prompt
	Last        *api.OutcomeStatus
	PRs         []api.PRStatus
	Approvals   []api.ApprovalStatus
	Runs        int // run.completed seen
	PRsOpened   int // pr.opened seen
	Archived    bool
	Proposal    string // an open "work in a sandbox?" proposal id
	modeByPrmpt map[string]string
}

// New returns an empty state.
func New() *State { return &State{modeByPrmpt: map[string]string{}} }

// Idle is true when nothing is running and nothing is queued.
func (s *State) Idle() bool { return s.Active == nil && len(s.Queued) == 0 }

func decode(ev api.Event, v any) { _ = json.Unmarshal(ev.Payload, v) }

// Apply folds one event in. Unknown event types are ignored.
func (s *State) Apply(ev api.Event) {
	if ev.Seq > s.LastSeq {
		s.LastSeq = ev.Seq
	}
	switch ev.Type {
	case "session.created":
		var p struct {
			Stage string `json:"stage"`
		}
		decode(ev, &p)
		s.Stage = "working"
		if p.Stage == "chat" {
			s.Stage = "chat"
		}
	case "session.working":
		s.Stage, s.Proposal = "working", ""
	case "conversation.work_proposed":
		var p struct {
			ProposalID string `json:"proposalId"`
		}
		decode(ev, &p)
		s.Proposal = p.ProposalID
	case "session.archived":
		s.Archived = true
	case "session.restored":
		s.Archived = false
	case "sandbox.status":
		var p struct {
			Status string `json:"status"`
		}
		decode(ev, &p)
		s.Sandbox = p.Status
	case "prompt.queued":
		var p struct {
			PromptID string `json:"promptId"`
			AuthorID string `json:"authorId"`
			Text     string `json:"text"`
			Mode     string `json:"mode"`
		}
		decode(ev, &p)
		s.Queued = append(s.Queued, Prompt{p.PromptID, p.AuthorID, p.Text, p.Mode})
		s.modeByPrmpt[p.PromptID] = p.Mode
	case "prompt.started":
		var p struct {
			PromptID string `json:"promptId"`
			RunID    string `json:"runId"`
		}
		decode(ev, &p)
		s.dequeue(p.PromptID)
		s.Active = &Run{ID: p.RunID, Mode: s.modeByPrmpt[p.PromptID], Stage: "starting"}
	case "prompt.cancelled":
		var p struct {
			PromptID string `json:"promptId"`
		}
		decode(ev, &p)
		s.dequeue(p.PromptID)
	case "run.accepted":
		s.setStage("working")
	case "tool.call", "agent.message.delta", "agent.message":
		if s.Active != nil && (s.Active.Stage == "starting" || s.Active.Stage == "waiting") {
			s.Active.Stage = "working"
		}
	case "verification.started":
		s.setStage("verifying")
	case "verification.completed":
		var p struct {
			Fixing bool `json:"fixing"`
		}
		decode(ev, &p)
		if p.Fixing {
			s.setStage("working")
		}
	case "review.started":
		s.setStage("reviewing")
	case "review.completed":
		s.setStage("working")
	case "run.completed":
		var rc api.RunCompleted
		decode(ev, &rc)
		s.Runs++
		s.Last = &api.OutcomeStatus{RunID: rc.RunID, Outcome: rc.Outcome, Summary: rc.Summary, At: ev.At}
		s.Active = nil
	case "pr.opened":
		var p struct {
			URL    string `json:"url"`
			Number int    `json:"number"`
			Repo   string `json:"repo"`
			Draft  bool   `json:"draft"`
		}
		decode(ev, &p)
		s.PRsOpened++
		s.upsertPR(api.PRStatus{Repo: p.Repo, Number: p.Number, URL: p.URL, State: "open", Draft: p.Draft})
	case "pr.updated":
		var p struct {
			URL    string `json:"url"`
			Number int    `json:"number"`
			Repo   string `json:"repo"`
			State  string `json:"state"`
		}
		decode(ev, &p)
		for i := range s.PRs {
			if s.PRs[i].Number == p.Number && (s.PRs[i].Repo == p.Repo || s.PRs[i].Repo == "" || p.Repo == "") {
				switch p.State {
				case "merged", "closed", "draft", "ready", "reopened":
					s.PRs[i].State = map[string]string{"draft": "open", "ready": "open", "reopened": "open"}[p.State]
					if s.PRs[i].State == "" {
						s.PRs[i].State = p.State
					}
					s.PRs[i].Draft = p.State == "draft"
				}
				return
			}
		}
	case "mcp.approval_requested":
		var p struct {
			RequestID string `json:"requestId"`
			Server    string `json:"server"`
			Tool      string `json:"tool"`
			ExpiresAt string `json:"expiresAt"`
		}
		decode(ev, &p)
		s.Approvals = append(s.Approvals, api.ApprovalStatus{Kind: "tool", RequestID: p.RequestID, What: p.Server + "." + p.Tool, ExpiresAt: p.ExpiresAt})
		s.setStage("waiting")
	case "network.access_requested":
		var p struct {
			RequestID string `json:"requestId"`
			Host      string `json:"host"`
			Access    string `json:"access"`
			ExpiresAt string `json:"expiresAt"`
		}
		decode(ev, &p)
		s.Approvals = append(s.Approvals, api.ApprovalStatus{Kind: "network", RequestID: p.RequestID, What: p.Host + " (" + p.Access + ")", ExpiresAt: p.ExpiresAt})
		s.setStage("waiting")
	case "mcp.approval_decided", "network.access_decided":
		var p struct {
			RequestID string `json:"requestId"`
		}
		decode(ev, &p)
		kept := s.Approvals[:0]
		for _, a := range s.Approvals {
			if a.RequestID != p.RequestID {
				kept = append(kept, a)
			}
		}
		s.Approvals = kept
		if len(s.Approvals) == 0 && s.Active != nil && s.Active.Stage == "waiting" {
			s.Active.Stage = "working"
		}
	}
}

func (s *State) setStage(stage string) {
	if s.Active != nil {
		s.Active.Stage = stage
	}
}

func (s *State) dequeue(id string) {
	kept := s.Queued[:0]
	for _, q := range s.Queued {
		if q.ID != id {
			kept = append(kept, q)
		}
	}
	s.Queued = kept
}

func (s *State) upsertPR(p api.PRStatus) {
	for i := range s.PRs {
		if s.PRs[i].Number == p.Number && s.PRs[i].Repo == p.Repo {
			s.PRs[i] = p
			return
		}
	}
	s.PRs = append(s.PRs, p)
}

// Status renders the state as the status answer. summary supplies what events can't: the
// session's own status word and title; empty keeps the derived ones.
func (s *State) Status(id, title, status string) *api.Status {
	st := &api.Status{ID: id, Title: title, Status: status, Stage: s.Stage, QueuedPrompts: len(s.Queued),
		LastOutcome: s.Last, PullRequests: s.PRs, Approvals: s.Approvals, Sandbox: s.Sandbox, LastSeq: s.LastSeq}
	if st.PullRequests == nil {
		st.PullRequests = []api.PRStatus{}
	}
	if s.Active != nil {
		st.Run = &api.RunStatus{RunID: s.Active.ID, Mode: s.Active.Mode, Stage: s.Active.Stage}
	}
	if st.Status == "" {
		switch {
		case s.Active != nil:
			st.Status = "running"
		case strings.EqualFold(s.Sandbox, "error"):
			st.Status = "failed"
		case s.Sandbox == "idle" || s.Sandbox == "terminated":
			st.Status = "sleeping"
		default:
			st.Status = "idle"
		}
	}
	return st
}
