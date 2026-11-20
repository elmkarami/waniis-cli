package sessionstate

import (
	"encoding/json"
	"testing"

	"github.com/elmkarami/waniis-cli/internal/api"
)

func ev(seq int, typ string, p any) api.Event {
	b, _ := json.Marshal(p)
	return api.Event{Seq: seq, Type: typ, At: "2026-11-20T10:00:00Z", Payload: b}
}

type m = map[string]any

func TestFold(t *testing.T) {
	s := New()
	s.Apply(ev(1, "prompt.queued", m{"promptId": "p1", "authorId": "u", "text": "a", "mode": "build"}))
	s.Apply(ev(2, "prompt.queued", m{"promptId": "p2", "authorId": "u", "text": "b", "mode": "ask"}))
	if s.Idle() || len(s.Queued) != 2 {
		t.Fatalf("%+v", s)
	}
	s.Apply(ev(3, "prompt.started", m{"promptId": "p1", "runId": "r1"}))
	if s.Active == nil || s.Active.Mode != "build" || len(s.Queued) != 1 || s.Active.Stage != "starting" {
		t.Fatalf("%+v", s.Active)
	}
	s.Apply(ev(4, "verification.started", m{}))
	if s.Active.Stage != "verifying" {
		t.Fatal(s.Active.Stage)
	}
	s.Apply(ev(5, "network.access_requested", m{"requestId": "n1", "host": "x.com", "access": "read"}))
	if len(s.Approvals) != 1 || s.Active.Stage != "waiting" {
		t.Fatalf("%+v", s)
	}
	s.Apply(ev(6, "network.access_decided", m{"requestId": "n1"}))
	if len(s.Approvals) != 0 || s.Active.Stage != "working" {
		t.Fatalf("%+v", s)
	}
	s.Apply(ev(7, "pr.opened", m{"url": "u", "number": 3, "repo": "a/b"}))
	s.Apply(ev(8, "run.completed", m{"runId": "r1", "outcome": "success", "summary": "ok"}))
	if s.Active != nil || s.Last.Outcome != "success" || s.Idle() || s.PRsOpened != 1 {
		t.Fatalf("%+v", s)
	}
	s.Apply(ev(9, "prompt.cancelled", m{"promptId": "p2"}))
	s.Apply(ev(10, "pr.updated", m{"repo": "a/b", "number": 3, "state": "merged"}))
	if !s.Idle() || s.PRs[0].State != "merged" || s.LastSeq != 10 {
		t.Fatalf("%+v", s)
	}
	st := s.Status()
	if st.Activity != "idle" || st.Run != nil || st.LastRun.Summary != "ok" {
		t.Fatalf("%+v", st)
	}
}
