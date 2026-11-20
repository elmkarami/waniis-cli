package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/output"
	"github.com/elmkarami/waniis-cli/internal/sessionstate"
	"github.com/elmkarami/waniis-cli/internal/stream"
)

const defaultTail = 100

// ---- logs / watch ----

func (a *app) sessionsLogsCmd() *cobra.Command {
	var tail, since int
	var all, follow, timestamps bool
	var untilFlag string
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "logs <id>",
		Short: "Read what a session did, and follow it live",
		Long: `Print a session's events as readable lines: prompts, the agent's answers, tool calls,
checks, pull requests, blocked network requests and what is waiting for approval. The last 100
events by default; --tail N changes that, --all prints the whole session, --since SEQ prints what
came after an event you already saw. --follow keeps streaming until Ctrl-C. --until ends it when a
run ends (run-end), the session has nothing running or queued (idle) or a pull request is open
(pr-open), with exit code 10 when the run failed or was stopped, and 8 on --timeout. --until
implies --follow.

With --json every line is one event as the API sends it (NDJSON), for scripts and agents.
Charts in answers are drawn on a terminal (see --charts) and always come with a table.`,
		Example: `  waniis sessions logs ses_01abc                 # the last 100 events
  waniis sessions logs ses_01abc --tail 60
  waniis sessions logs ses_01abc --all --json     # every event, one JSON object per line
  waniis sessions logs ses_01abc -f --until run-end
  waniis logs ses_01abc --since 412 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			u, err := parseUntil(untilFlag)
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			if all && (fl.Changed("tail") || fl.Changed("since")) {
				return exitcode.Usagef("--all can't be combined with --tail or --since")
			}
			if fl.Changed("tail") && fl.Changed("since") {
				return exitcode.Usagef("use --tail or --since, not both")
			}
			if tail < 1 {
				return exitcode.Usagef("--tail must be at least 1")
			}
			if since < 0 {
				return exitcode.Usagef("--since must not be negative")
			}
			if u != untilNone {
				follow = true
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			needState := u != untilNone
			var full, shown []api.Event
			switch {
			case all || needState || fl.Changed("since"):
				if full, err = stream.History(ctx, rt.Client, id, 0); err != nil {
					return err
				}
				shown = full
				switch {
				case fl.Changed("since"):
					shown = nil
					for _, ev := range full {
						if ev.Seq > since {
							shown = append(shown, ev)
						}
					}
				case !all:
					shown = full[max(0, len(full)-tail):]
				}
			default:
				if shown, err = stream.Tail(ctx, rt.Client, id, tail); err != nil {
					return err
				}
			}
			st := sessionstate.New()
			src := full
			if src == nil {
				src = shown
			}
			for _, ev := range src {
				st.Apply(ev)
			}
			emit, flush := a.sink(ctx, rt, id, false, timestamps)
			for _, ev := range shown {
				if err := emit(ev); err != nil {
					return err
				}
			}
			flush()
			if len(shown) == 0 && !a.useJSON() && !follow {
				fmt.Fprintln(a.Err, "No events.")
			}
			if !follow {
				return nil
			}
			live, flush := a.sink(ctx, rt, id, true, timestamps)
			defer flush()
			return a.follow(ctx, rt, followSpec{ID: id, Since: st.LastSeq, State: st,
				Cond: newCondition(u, "", false, st), OnEvent: live, Timeout: timeout})
		},
	}
	fl := c.Flags()
	fl.IntVar(&tail, "tail", defaultTail, "show the last N events")
	fl.IntVar(&since, "since", 0, "show events after this sequence number")
	fl.BoolVar(&all, "all", false, "show the whole session")
	fl.BoolVarP(&follow, "follow", "f", false, "keep streaming new events until Ctrl-C")
	fl.StringVar(&untilFlag, "until", "", "end when: run-end, idle or pr-open (implies --follow)")
	fl.DurationVar(&timeout, "timeout", 0, "with --follow: give up after this long (exit 8); 0 waits forever")
	fl.BoolVar(&timestamps, "timestamps", false, "prefix each line with its time (UTC)")
	return c
}

func (a *app) watchCmd() *cobra.Command {
	c := a.sessionsLogsCmd()
	c.Use = "watch <id>"
	c.Aliases = nil
	c.Short = "Follow a session live (logs --follow)"
	c.Long = "Follow a session live: the same as `waniis logs <id> --follow`. Ctrl-C stops watching; the session keeps running."
	c.Example = "  waniis watch ses_01abc\n  waniis watch ses_01abc --until pr-open"
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		_ = cmd.Flags().Set("follow", "true")
		return run(cmd, args)
	}
	return c
}

// ---- status ----

func (a *app) sessionsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Is it running? The current run, queue, last outcome and PRs in one call",
		Example: `  waniis sessions status ses_01abc
  waniis sessions status ses_01abc --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			st, raw, err := a.fetchStatus(cmd.Context(), rt, args[0])
			if err != nil {
				return err
			}
			if a.useJSON() {
				if raw != nil {
					return output.JSON(a.Out, raw)
				}
				return output.JSON(a.Out, st)
			}
			return a.writeStatus(rt, args[0], st)
		},
	}
}

// fetchStatus asks the one-call status route; a server without it is answered from the latest
// events.
func (a *app) fetchStatus(ctx context.Context, rt *conn, id string) (*api.Status, json.RawMessage, error) {
	st, raw, err := rt.Client.GetStatus(ctx, id)
	if err == nil {
		return st, raw, nil
	}
	if !isNotFound(err) {
		return nil, nil, err
	}
	s, err := rt.Client.GetSession(ctx, id) // a real 404 stops here
	if err != nil {
		return nil, nil, err
	}
	evs, err := stream.Tail(ctx, rt.Client, id, 300)
	if err != nil {
		return nil, nil, err
	}
	state := sessionstate.New()
	for _, ev := range evs {
		state.Apply(ev)
	}
	st = state.Status()
	if s.Stage != "" {
		st.Stage = s.Stage
	}
	if st.PullRequest == nil && s.PullRequest != nil {
		st.PullRequest = &api.PRStatus{Number: s.PullRequest.Number, URL: s.PullRequest.URL}
	}
	return st, nil, nil
}

func (a *app) writeStatus(rt *conn, id string, st *api.Status) error {
	run := "-"
	if st.Run != nil {
		run = "going"
		var bits []string
		if st.Run.Stage != "" {
			bits = append(bits, st.Run.Stage)
		}
		if st.Run.Mode != "" {
			bits = append(bits, st.Run.Mode)
		}
		if len(bits) > 0 {
			run += " (" + strings.Join(bits, ", ") + ")"
		}
	}
	last := "-"
	if o := st.LastRun; o != nil {
		last = o.Outcome
		if o.Summary != "" {
			last += ": " + output.Truncate(o.Summary, 160)
		}
	}
	pr := "-"
	if p := st.PullRequest; p != nil {
		pr = fmt.Sprintf("#%d %s", p.Number, p.URL)
		if p.Draft {
			pr += " (draft)"
		}
	}
	sandbox := "-"
	if st.Sandbox != nil {
		sandbox = st.Sandbox.Status
		if st.Sandbox.Detail != "" {
			sandbox += " (" + st.Sandbox.Detail + ")"
		}
	}
	return output.KeyValues(a.Out, [][2]string{
		{"Session", id}, {"Activity", orDash(st.Activity)}, {"Stage", orDash(st.Stage)}, {"Run", run},
		{"Queued prompts", fmt.Sprint(st.Queued)}, {"Last run", last}, {"Pull request", pr},
		{"Sandbox", sandbox}, {"URL", sessionURL(rt.Base, id)},
	})
}

// ---- prompt ----

type promptResult struct {
	ID       string `json:"id"`
	PromptID string `json:"promptId,omitempty"`
	RunID    string `json:"runId,omitempty"`
	Queued   bool   `json:"queued"`
	Position int    `json:"position"`
	Outcome  string `json:"outcome,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

func (a *app) sessionsPromptCmd() *cobra.Command {
	var mode, model, key string
	var wait, follow bool
	var waitTimeout time.Duration
	c := &cobra.Command{
		Use:   "prompt <id> \"<text>\"",
		Short: "Add a prompt to a session (queued if a run is going, starts one if it is idle)",
		Long: `Append a prompt to a session. If a run is going the prompt is queued behind it; if the
session is idle a new run starts. The text can be "-" to read it from stdin. --wait blocks until
this prompt's run ends (exit 10 if it failed or was stopped); --follow also streams it. The prompt
is sent with an Idempotency-Key, so a retry never sends it twice.`,
		Example: `  waniis sessions prompt ses_01abc "also add a test for the empty cart"
  echo "rebase on main" | waniis sessions prompt ses_01abc - --wait
  waniis sessions prompt ses_01abc "explain the retry logic" --mode ask --follow`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			text, err := a.readPrompt(args[1])
			if err != nil {
				return err
			}
			if text == "" {
				return exitcode.Usagef("the prompt is empty")
			}
			if err := oneOf("mode", mode, validModes); err != nil {
				return err
			}
			if wait && follow {
				return exitcode.Usagef("use --wait or --follow, not both")
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			baseline := 0
			if wait || follow {
				if evs, err := stream.Tail(ctx, rt.Client, id, 1); err == nil && len(evs) > 0 {
					baseline = evs[0].Seq
				}
			}
			if key == "" {
				key = a.NewKey()
			}
			res, err := rt.Client.SubmitPrompt(ctx, id, api.PromptCommand{Text: text, Mode: mode, Model: model}, key)
			if err != nil {
				return err
			}
			out := &promptResult{ID: id, PromptID: res.PromptID, RunID: res.RunID}
			if res.Queued != nil {
				out.Queued = *res.Queued
			}
			if res.Position != nil {
				out.Position = *res.Position
				out.Queued = out.Queued || out.Position > 0
			}
			if !a.useJSON() && !follow {
				if out.Queued {
					fmt.Fprintf(a.Out, "Queued behind the running work (position %d).\n", out.Position)
				} else {
					fmt.Fprintln(a.Out, "Prompt sent.")
				}
			}
			if !wait && !follow {
				if a.useJSON() {
					return output.JSON(a.Out, out)
				}
				return nil
			}
			st, werr := a.waitFor(ctx, rt, id, waitSpec{Until: untilRunEnd, PromptID: out.PromptID, ExpectRun: true,
				Baseline: baseline, Print: follow, Timeout: waitTimeout})
			if werr != nil && exitcode.Of(werr) != exitcode.RunFailed {
				return werr
			}
			if st != nil {
				if o := st.Outcomes[st.RunOf[out.PromptID]]; o != nil {
					out.Outcome, out.Summary, out.RunID = o.Outcome, o.Summary, o.ID
				} else if st.Last != nil {
					out.Outcome, out.Summary = st.Last.Outcome, st.Last.Summary
				}
			}
			if !follow || !a.useJSON() {
				if a.useJSON() {
					if err := output.JSON(a.Out, out); err != nil {
						return err
					}
				} else if wait && out.Outcome != "" {
					fmt.Fprintf(a.Out, "Run %s: %s\n", out.Outcome, out.Summary)
				}
			}
			return werr
		},
	}
	fl := c.Flags()
	fl.StringVar(&mode, "mode", "", "auto|build|plan|ask (default: the agent decides; ask is read-only)")
	fl.StringVar(&model, "model", "", "model for this prompt's run, one the workspace allows")
	fl.StringVar(&key, "idempotency-key", "", "key that makes retries safe (default: a random one)")
	fl.BoolVar(&wait, "wait", false, "wait for this prompt's run to end")
	fl.BoolVar(&follow, "follow", false, "stream the run until it ends")
	fl.DurationVar(&waitTimeout, "wait-timeout", 30*time.Minute, "give up waiting after this long (exit 8)")
	return c
}

// ---- steer ----

func (a *app) sessionsSteerCmd() *cobra.Command {
	var key string
	c := &cobra.Command{
		Use:   "steer <id> \"<text>\"",
		Short: "Steer the run that is going (the agent reads it at its next step)",
		Long: `Send a message to the running run to change its course. It fails when nothing is running;
use "waniis sessions prompt" to add work to an idle session or to queue it. The text can be "-".`,
		Example: `  waniis sessions steer ses_01abc "don't touch the migration files"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			text, err := a.readPrompt(args[1])
			if err != nil {
				return err
			}
			if text == "" {
				return exitcode.Usagef("the steering text is empty")
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			if key == "" {
				key = a.NewKey()
			}
			// Steering needs a run: check the status first, as the server has no dedicated error.
			if st, _, serr := a.fetchStatus(cmd.Context(), rt, id); serr == nil && st.Activity == "idle" {
				return steerError(nil, id)
			}
			res, err := rt.Client.Steer(cmd.Context(), id, text, key)
			if err != nil {
				return steerError(err, id)
			}
			if a.useJSON() {
				return output.JSON(a.Out, map[string]any{"id": id, "runId": res.RunID, "steered": true})
			}
			fmt.Fprintln(a.Out, "Steering sent.")
			return nil
		},
	}
	c.Flags().StringVar(&key, "idempotency-key", "", "key that makes retries safe (default: a random one)")
	return c
}

// steerError says plainly that nothing is running, and what to do instead. err is nil when the
// status already said so; any other 4xx about the command gets the same hint.
func steerError(err error, id string) error {
	hint := fmt.Sprintf("Use `waniis sessions prompt %s \"...\"` to send it as a new prompt.", id)
	if err == nil {
		return exitcode.New(exitcode.Conflict, "no_active_run", "Nothing is running in this session to steer.").WithHint(hint)
	}
	ee := exitcode.As(err)
	if ee.Status >= 400 && ee.Status < 500 && ee.Code != exitcode.Auth && ee.Code != exitcode.NotFound && ee.Code != exitcode.Billing {
		return exitcode.New(exitcode.Conflict, "no_active_run", "The run couldn't be steered: "+ee.Message).WithHint(hint)
	}
	return err
}

// ---- wait ----

func (a *app) sessionsWaitCmd() *cobra.Command {
	var untilFlag string
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "wait <id>",
		Short: "Block until a run ends (or the session is idle, or a PR is open)",
		Long: `Wait for a session. --until run-end (the default) returns when the current run ends, or at
once when nothing is running; idle waits until nothing is running or queued; pr-open waits until
a pull request is open. The exit code says how it ended: 0 for success, 10 if the run failed or
was stopped, 8 on --timeout.`,
		Example: `  waniis sessions wait ses_01abc --timeout 20m
  waniis sessions wait ses_01abc --until pr-open --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if untilFlag == "" {
				untilFlag = "run-end"
			}
			u, err := parseUntil(untilFlag)
			if err != nil {
				return err
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			st, werr := a.waitFor(cmd.Context(), rt, id, waitSpec{Until: u, Timeout: timeout})
			if werr != nil && exitcode.Of(werr) != exitcode.RunFailed && exitcode.As(werr).APICode != "no_pull_request" {
				return werr
			}
			status := st.Status()
			prs := st.PRs
			if prs == nil {
				prs = []api.PRStatus{}
			}
			res := map[string]any{"id": id, "until": string(u), "url": sessionURL(rt.Base, id), "pullRequests": prs,
				"status": status}
			if o := st.Last; o != nil {
				res["outcome"], res["summary"] = o.Outcome, o.Summary
			}
			if a.useJSON() {
				if err := output.JSON(a.Out, res); err != nil {
					return err
				}
			} else {
				if o := st.Last; o != nil {
					fmt.Fprintf(a.Out, "Run %s: %s\n", o.Outcome, o.Summary)
				} else {
					fmt.Fprintln(a.Out, "Nothing has run in this session yet.")
				}
				for _, p := range st.PRs {
					fmt.Fprintf(a.Out, "Pull request: %s\n", p.URL)
				}
			}
			return werr
		},
	}
	c.Flags().StringVar(&untilFlag, "until", "", "run-end (default), idle or pr-open")
	c.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (exit 8); 0 waits forever")
	return c
}
