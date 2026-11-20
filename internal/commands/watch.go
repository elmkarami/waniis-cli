package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/render"
	"github.com/elmkarami/waniis-cli/internal/sessionstate"
	"github.com/elmkarami/waniis-cli/internal/stream"
	"github.com/elmkarami/waniis-cli/internal/style"
)

// ---- output helpers shared by the watching commands ----

func (a *app) termWidth() int {
	if a.TermWidth != nil {
		if w := a.TermWidth(); w > 0 {
			return w
		}
	}
	return 80
}

func (a *app) colorOn() bool { return a.StdoutTTY && !a.noColor() }

// chartsOn says whether charts are drawn above their tables (--charts).
func (a *app) chartsOn() bool {
	switch a.g.Charts {
	case "always":
		return true
	case "never":
		return false
	}
	return a.StdoutTTY
}

func (a *app) renderOpts(live bool, names map[string]string) render.Options {
	return render.Options{Style: style.Style{On: a.colorOn()}, Width: a.termWidth(), Charts: a.chartsOn(), Live: live, Names: names}
}

// names maps user ids on the session to their logins, so events read "(maya)". Best effort.
func (a *app) names(ctx context.Context, rt *conn, id string) map[string]string {
	people, err := rt.Client.People(ctx, id)
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, p := range people {
		if p.Login != "" {
			m[p.ID] = p.Login
		} else {
			m[p.ID] = p.Name
		}
	}
	return m
}

// writeNDJSON writes an event as one line of the API's own JSON.
func writeNDJSON(w io.Writer, ev api.Event) error {
	raw := []byte(ev.Raw)
	if len(raw) == 0 {
		var err error
		if raw, err = json.Marshal(ev); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// sink prints events: NDJSON with --json, rendered lines otherwise. flush ends the output.
func (a *app) sink(ctx context.Context, rt *conn, id string, live bool, timestamps bool) (emit func(api.Event) error, flush func()) {
	if a.useJSON() {
		return func(ev api.Event) error { return writeNDJSON(a.Out, ev) }, func() {}
	}
	o := a.renderOpts(live, a.names(ctx, rt, id))
	o.Timestamps = timestamps
	r := render.New(a.Out, o)
	return func(ev api.Event) error { r.Event(ev); return nil }, r.Flush
}

// ---- until conditions ----

type until string

const (
	untilNone   until = ""
	untilRunEnd until = "run-end"
	untilIdle   until = "idle"
	untilPROpen until = "pr-open"
)

func parseUntil(s string) (until, error) {
	switch u := until(s); u {
	case untilNone, untilRunEnd, untilIdle, untilPROpen:
		return u, nil
	}
	return "", exitcode.Usagef("--until must be run-end, idle or pr-open")
}

// outcomeError maps a run's end to the exit codes: 10 when it failed or was stopped.
func outcomeError(o *api.OutcomeStatus) error {
	if o == nil {
		return nil
	}
	if o.Outcome == "failed" || o.Outcome == "stopped" {
		return exitcode.New(exitcode.RunFailed, "run_"+o.Outcome, "The run ended "+o.Outcome+".").WithHint(o.Summary)
	}
	return nil
}

// condition is what a follow or wait ends on.
type condition struct {
	kind      until
	promptID  string // wait for this prompt's run instead of "the current run"
	expectRun bool   // a run is about to start: an empty log isn't "idle"
	busy      bool
	startRuns int
}

func newCondition(kind until, promptID string, expectRun bool, st *sessionstate.State) *condition {
	if kind == untilNone && promptID == "" {
		return nil
	}
	return &condition{kind: kind, promptID: promptID, expectRun: expectRun, busy: expectRun || !st.Idle(), startRuns: st.Runs}
}

// done reports whether the condition is met, and the error that gives the exit code.
func (c *condition) done(st *sessionstate.State) (bool, error) {
	if c == nil {
		return false, nil
	}
	settled := st.Idle() && (!c.expectRun || st.Runs > 0)
	if c.promptID != "" {
		if st.Cancelled[c.promptID] {
			return true, exitcode.New(exitcode.RunFailed, "run_stopped", "The prompt was cancelled before it ran.")
		}
		if o := st.Outcomes[st.RunOf[c.promptID]]; st.RunOf[c.promptID] != "" && o != nil {
			return true, outcomeError(o)
		}
		return false, nil
	}
	switch c.kind {
	case untilRunEnd:
		if !c.busy || st.Runs > c.startRuns {
			return true, outcomeError(st.Last)
		}
	case untilIdle:
		if settled {
			return true, outcomeError(st.Last)
		}
	case untilPROpen:
		if st.PRsOpened > 0 {
			return true, nil
		}
		if settled {
			if err := outcomeError(st.Last); err != nil {
				return true, err
			}
			return true, exitcode.New(exitcode.General, "no_pull_request", "The session went idle without opening a pull request.")
		}
	}
	return false, nil
}

// ---- following ----

type followSpec struct {
	ID      string
	Since   int // stream events after this seq
	State   *sessionstate.State
	Cond    *condition
	OnEvent func(api.Event) error
	Timeout time.Duration // 0: no limit
}

// follow streams the session's events into spec.State and spec.OnEvent until the condition is met
// (nil error unless the condition says the run failed), the timeout passes (exit 8) or ctx ends.
func (a *app) follow(ctx context.Context, rt *conn, spec followSpec) error {
	if ok, err := spec.Cond.done(spec.State); ok {
		return err
	}
	fctx := ctx
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		fctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	var result error
	err := stream.Follow(fctx, rt.Client, spec.ID, stream.Options{
		Since:        spec.Since,
		Sleep:        a.Sleep,
		PollInterval: a.PollInterval,
		Notice:       func(m string) { fmt.Fprintln(a.Err, "waniis: "+m) },
	}, func(ev api.Event) error {
		spec.State.Apply(ev)
		if spec.OnEvent != nil {
			if err := spec.OnEvent(ev); err != nil {
				return err
			}
		}
		if ok, err := spec.Cond.done(spec.State); ok {
			result = err
			return stream.ErrStop
		}
		return nil
	})
	if err != nil {
		if spec.Timeout > 0 && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return exitcode.New(exitcode.Timeout, "wait_timeout", "Timed out waiting.").
				WithHint("The session keeps running: " + sessionURL(rt.Base, spec.ID))
		}
		return err
	}
	return result
}

// waitSpec drives waitFor.
type waitSpec struct {
	Until      until
	PromptID   string
	ExpectRun  bool
	Baseline   int  // print events after this seq (when Print)
	Print      bool // show the events as they happen
	Timestamps bool
	Timeout    time.Duration
}

// waitFor reads the session's log, then follows it until the condition is met. It returns the
// state at the end, and an error carrying the exit code.
func (a *app) waitFor(ctx context.Context, rt *conn, id string, w waitSpec) (*sessionstate.State, error) {
	hist, err := stream.History(ctx, rt.Client, id, 0)
	if err != nil {
		return nil, err
	}
	st := sessionstate.New()
	var emit func(api.Event) error
	flush := func() {}
	if w.Print {
		emit, flush = a.sink(ctx, rt, id, false, w.Timestamps)
	}
	for _, ev := range hist {
		st.Apply(ev)
		if emit != nil && ev.Seq > w.Baseline {
			if err := emit(ev); err != nil {
				return st, err
			}
		}
	}
	flush()
	var live func(api.Event) error
	if w.Print {
		live, flush = a.sink(ctx, rt, id, true, w.Timestamps)
	}
	err = a.follow(ctx, rt, followSpec{ID: id, Since: st.LastSeq, State: st,
		Cond: newCondition(w.Until, w.PromptID, w.ExpectRun, st), OnEvent: live, Timeout: w.Timeout})
	flush()
	return st, err
}

func isNotFound(err error) bool {
	var ee *exitcode.Error
	return errors.As(err, &ee) && ee.Code == exitcode.NotFound
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
