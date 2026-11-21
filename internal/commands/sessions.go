package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/config"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/output"
)

// maxPageSize is the API's largest page (G4 keeps the spec's 100 cap).
const maxPageSize = 100

func (a *app) sessionsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"s", "session"},
		Short:   "Start, list, search, stop, share and open sessions",
	}
	c.AddCommand(a.sessionsListCmd(), a.sessionsShowCmd(), a.sessionsStartCmd(), a.sessionsStopCmd(), a.sessionsPRCmd(),
		a.sessionsSearchCmd(), a.sessionsShareCmd(), a.sessionsOpenCmd(),
		a.sessionsLogsCmd(), a.sessionsStatusCmd(), a.sessionsPromptCmd(), a.sessionsSteerCmd(), a.sessionsWaitCmd())
	return c
}

// sessionURL is the page for a session in the web app.
func sessionURL(base, id string) string {
	return config.AppURL(base) + "/s/" + url.PathEscape(id)
}

// ago renders a time as a short relative age, or an absolute date when it is old.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return t.UTC().Format("2006-01-02")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.UTC().Format("2006-01-02")
}

func repoNames(s api.Session) string {
	names := make([]string, 0, len(s.Repos))
	for _, r := range s.Repos {
		names = append(names, r.FullName)
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ",")
}

func prLabel(s api.Session) string {
	if s.PullRequest == nil {
		return "-"
	}
	return fmt.Sprintf("#%d", s.PullRequest.Number)
}

func (a *app) sessionRows(items []api.Session) [][]string {
	rows := make([][]string, 0, len(items))
	for _, s := range items {
		rows = append(rows, []string{s.ID, orDash(s.Status), orDash(s.Stage), repoNames(s), prLabel(s),
			ago(a.Now(), s.LastActiveAt), output.Truncate(s.Title, 60)})
	}
	return rows
}

var sessionHeaders = []string{"ID", "STATUS", "STAGE", "REPO", "PR", "UPDATED", "TITLE"}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// listFlags are the filters shared by `list`, `search` and `export sessions`.
type listFlags struct {
	filter, author, stage, since, until string
	status, repo                        []string
	hasPR, all                          bool
	limit                               int
	cursor                              string
}

func addListFlags(c *cobra.Command, f *listFlags, withPaging bool) {
	fl := c.Flags()
	fl.StringVar(&f.filter, "filter", "", "whose sessions: mine|shared|archived|all (default all)")
	fl.StringSliceVar(&f.status, "status", nil, "only these statuses (running|idle|sleeping|failed); repeatable")
	fl.StringSliceVar(&f.repo, "repo", nil, "only these repos (owner/name); repeatable")
	fl.StringVar(&f.author, "author", "", "who started it: me or a login")
	fl.StringVar(&f.stage, "stage", "", "chat|working")
	fl.StringVar(&f.since, "since", "", "active since (ISO 8601 date or time)")
	fl.StringVar(&f.until, "until", "", "active until (ISO 8601 date or time)")
	fl.BoolVar(&f.hasPR, "has-pr", false, "only sessions that opened a pull request")
	if withPaging {
		fl.IntVar(&f.limit, "limit", 0, "page size, up to 100 (default 30 on a terminal, 100 otherwise)")
		fl.StringVar(&f.cursor, "cursor", "", "continue from this cursor")
		fl.BoolVar(&f.all, "all", false, "follow every page")
	}
}

func (a *app) toFilter(f listFlags) (api.ListFilter, error) {
	switch f.filter {
	case "", "mine", "shared", "archived", "all":
	default:
		return api.ListFilter{}, exitcode.Usagef("--filter must be mine, shared, archived or all")
	}
	switch f.stage {
	case "", "chat", "working":
	default:
		return api.ListFilter{}, exitcode.Usagef("--stage must be chat or working")
	}
	if f.limit < 0 {
		return api.ListFilter{}, exitcode.Usagef("--limit must be positive")
	}
	limit := f.limit
	if limit == 0 {
		limit = 100
		if a.interactive() {
			limit = 30
		}
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	return api.ListFilter{Filter: f.filter, Status: f.status, Repo: f.repo, Author: f.author, Stage: f.stage,
		Since: f.since, Until: f.until, HasPR: f.hasPR, Limit: limit, Cursor: f.cursor}, nil
}

// pageFunc fetches one page.
type pageFunc func(ctx context.Context, f api.ListFilter) (*api.SessionPage, error)

// collect fetches one page, or every page with all, following the cursor.
func (a *app) collect(ctx context.Context, fetch pageFunc, f api.ListFilter, all bool) ([]api.Session, string, *api.Interpretation, error) {
	var items []api.Session
	var interp *api.Interpretation
	for pageNo := 1; ; pageNo++ {
		page, err := fetch(ctx, f)
		if err != nil {
			return nil, "", nil, err
		}
		if page.Interpreted != nil && interp == nil {
			interp = page.Interpreted
		}
		items = append(items, page.All()...)
		next := page.Cursor()
		if !all || next == "" {
			return items, next, interp, nil
		}
		if next == f.Cursor {
			return nil, "", nil, exitcode.New(exitcode.General, "bad_response", "the API returned the same cursor twice")
		}
		if a.interactive() {
			fmt.Fprintf(a.Err, "\rFetched %d sessions…", len(items))
		}
		f.Cursor = next
	}
}

func (a *app) writeSessionList(items []api.Session, next string, interp *api.Interpretation) error {
	if a.useJSON() {
		out := map[string]any{"items": nonNil(items), "next": nullable(next)}
		if interp != nil {
			out["interpreted"] = interp
		}
		return output.JSON(a.Out, out)
	}
	if len(items) == 0 {
		fmt.Fprintln(a.Err, "No sessions found.")
		return nil
	}
	if err := output.Table(a.Out, sessionHeaders, a.sessionRows(items)); err != nil {
		return err
	}
	if next != "" {
		fmt.Fprintf(a.Err, "More sessions are available: use --all, or --cursor %s\n", next)
	}
	return nil
}

func nonNil(s []api.Session) []api.Session {
	if s == nil {
		return []api.Session{}
	}
	return s
}

func (a *app) sessionsListCmd() *cobra.Command {
	var f listFlags
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the sessions you can see",
		Example: `  waniis sessions list --status running
  waniis sessions list --repo acme/api --has-pr --since 2026-11-01
  waniis sessions list --all --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := a.toFilter(f)
			if err != nil {
				return err
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			items, next, _, err := a.collect(cmd.Context(), rt.Client.ListSessions, filter, f.all)
			if err != nil {
				return err
			}
			return a.writeSessionList(items, next, nil)
		},
	}
	addListFlags(c, &f, true)
	return c
}

func (a *app) sessionsSearchCmd() *cobra.Command {
	var f listFlags
	var noInterpret bool
	c := &cobra.Command{
		Use:   "search \"<natural language>\"",
		Short: "Search sessions in plain language",
		Long: `Search the sessions you can see. Waniis turns the text into filters (repos, people,
status, dates, pull request state, words to match) and says how it understood you. Flags you give
win over that interpretation; --no-interpret matches the words only.`,
		Example: `  waniis sessions search "payment bugs fixed last week that have a merged PR"
  waniis sessions search "flaky checkout" --repo acme/api --no-interpret --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := strings.TrimSpace(args[0])
			if q == "" {
				return exitcode.Usagef("the search text is empty")
			}
			filter, err := a.toFilter(f)
			if err != nil {
				return err
			}
			filter.Query, filter.NoInterpret = q, noInterpret
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			items, next, interp, err := a.collect(cmd.Context(), rt.Client.SearchSessions, filter, f.all)
			if err != nil {
				return err
			}
			if !a.useJSON() && !noInterpret {
				fmt.Fprintln(a.Err, interpretationLine(interp))
			}
			return a.writeSessionList(items, next, interp)
		},
	}
	addListFlags(c, &f, true)
	c.Flags().BoolVar(&noInterpret, "no-interpret", false, "skip the model: match the words only")
	return c
}

// interpretationLine says how a search was understood, e.g.
// "Searching: merged PRs in acme/pay since 2026-11-11 matching 'payment'".
func interpretationLine(i *api.Interpretation) string {
	if i == nil {
		return "Searching: all sessions"
	}
	var parts []string
	if i.PRState != "" {
		parts = append(parts, i.PRState+" PRs")
	}
	if len(i.Status) > 0 {
		parts = append(parts, strings.Join(i.Status, "/")+" sessions")
	}
	if i.Stage != "" {
		parts = append(parts, i.Stage+" stage")
	}
	if len(i.Repos) > 0 {
		parts = append(parts, "in "+strings.Join(i.Repos, ", "))
	}
	if len(i.People) > 0 {
		parts = append(parts, "by "+strings.Join(i.People, ", "))
	}
	if i.Since != "" {
		parts = append(parts, "since "+i.Since)
	}
	if i.Until != "" {
		parts = append(parts, "until "+i.Until)
	}
	if i.Text != "" {
		parts = append(parts, "matching '"+i.Text+"'")
	}
	if len(parts) == 0 {
		return "Searching: all sessions"
	}
	return "Searching: " + strings.Join(parts, " ")
}

func (a *app) sessionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			s, err := rt.Client.GetSession(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.useJSON() {
				return output.JSON(a.Out, s)
			}
			pr := "-"
			if s.PullRequest != nil {
				pr = fmt.Sprintf("#%d %s", s.PullRequest.Number, s.PullRequest.URL)
			}
			return output.KeyValues(a.Out, [][2]string{
				{"ID", s.ID}, {"Title", orDash(s.Title)}, {"Status", orDash(s.Status)}, {"Stage", orDash(s.Stage)},
				{"Repos", repoNames(*s)}, {"Pull request", pr}, {"Visibility", orDash(s.Visibility)},
				{"Updated", ago(a.Now(), s.LastActiveAt)}, {"URL", sessionURL(rt.Base, s.ID)},
			})
		},
	}
}

// ---- start ----

var (
	validModes      = []string{"auto", "build", "plan", "ask"}
	validVisibility = []string{"private", "team", "workspace"}
	validRoles      = []string{"viewer", "collaborator"}
)

func oneOf(flag, v string, allowed []string) error {
	if v == "" {
		return nil
	}
	for _, x := range allowed {
		if v == x {
			return nil
		}
	}
	return exitcode.Usagef("--%s must be one of %s", flag, strings.Join(allowed, ", "))
}

var repoShort = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var repoURL = regexp.MustCompile(`^(?:https?://github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// ParseRepo turns "owner/name" or a GitHub remote URL into owner/name.
func ParseRepo(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := repoURL.FindStringSubmatch(s); m != nil {
		return m[1] + "/" + m[2], true
	}
	if repoShort.MatchString(s) && !strings.HasSuffix(s, ".git") {
		return s, true
	}
	return "", false
}

func cloneURL(fullName string) string { return "https://github.com/" + fullName + ".git" }

func (a *app) readPrompt(arg string) (string, error) {
	if arg != "-" {
		return strings.TrimSpace(arg), nil
	}
	b, err := io.ReadAll(io.LimitReader(a.In, 1<<20))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (a *app) sessionsStartCmd() *cobra.Command {
	var repos []string
	var branch, mode, model, visibility, team, title, key string
	var chat, wait, follow bool
	var waitTimeout time.Duration
	c := &cobra.Command{
		Use:     "start \"<prompt>\"",
		Aliases: []string{"new"},
		Short:   "Start a session with a first prompt",
		Long: `Start a session and send its first prompt. Inside a git checkout, the repo is the
origin remote's unless you pass --repo. Use --chat for a conversation with no repo. The prompt can
be "-" to read it from stdin.

Every start sends an Idempotency-Key, so a retry after a network error never starts two sessions.
--wait blocks until the first run ends; the exit code is 0 when it succeeded and 10 when it failed
or was stopped. --follow streams the first run (with --json: its events, one per line) and ends
the same way.`,
		Example: `  waniis sessions start "fix the flaky checkout test" --repo acme/api
  waniis sessions start --chat "how does the billing webhook retry?"
  waniis sessions start "bump deps" --repo acme/api --wait --json
  waniis new "fix the flaky checkout test" --follow`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := a.readPrompt(args[0])
			if err != nil {
				return err
			}
			if text == "" {
				return exitcode.Usagef("the prompt is empty")
			}
			for _, chk := range []error{oneOf("mode", mode, validModes), oneOf("visibility", visibility, validVisibility)} {
				if chk != nil {
					return chk
				}
			}
			if team != "" && visibility != "team" {
				return exitcode.Usagef("--team needs --visibility team")
			}
			if visibility == "team" && team == "" {
				return exitcode.Usagef("--visibility team needs --team")
			}
			if wait && follow {
				return exitcode.Usagef("use --wait or --follow, not both")
			}
			if chat && len(repos) > 0 {
				return exitcode.Usagef("--chat starts a conversation with no repo; drop --repo or --chat")
			}
			in := api.CreateSessionInput{Title: title, Visibility: visibility, TeamID: team,
				Prompt: &api.PromptInput{Type: "prompt.submit", Text: text, Mode: mode, Model: model, Source: "cli"}}
			if chat {
				in.Stage = "chat"
			} else {
				names, err := a.resolveRepos(repos)
				if err != nil {
					return err
				}
				for _, n := range names {
					in.Repos = append(in.Repos, api.RepoInput{CloneURL: cloneURL(n), Branch: branch})
				}
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			if key == "" {
				key = a.NewKey()
			}
			created, err := rt.Client.CreateSession(cmd.Context(), in, key)
			if err != nil {
				return err
			}
			result := &startResult{ID: created.ID, URL: sessionURL(rt.Base, created.ID)}
			if !a.useJSON() || follow {
				fmt.Fprintf(a.Err, "Started session %s\n", created.ID)
			}
			var runErr error
			if follow {
				fmt.Fprintln(a.Err, sessionURL(rt.Base, created.ID))
				st, ferr := a.waitFor(cmd.Context(), rt, created.ID, waitSpec{Until: untilRunEnd, ExpectRun: true, Print: true, Timeout: waitTimeout})
				if ferr != nil && exitcode.Of(ferr) != exitcode.RunFailed {
					return ferr
				}
				if st != nil && st.Last != nil {
					result.Outcome, result.Summary = st.Last.Outcome, st.Last.Summary
				}
				if !a.useJSON() {
					if s, err := rt.Client.GetSession(cmd.Context(), created.ID); err == nil && s.PullRequest != nil {
						fmt.Fprintf(a.Out, "Pull request: %s\n", s.PullRequest.URL)
					}
				}
				return ferr
			}
			if wait {
				runErr = a.waitForRun(cmd.Context(), rt, result, waitTimeout)
				if s, err := rt.Client.GetSession(cmd.Context(), created.ID); err == nil {
					result.Session = s
				}
			}
			if err := a.writeStart(result, wait); err != nil {
				return err
			}
			return runErr
		},
	}
	fl := c.Flags()
	fl.StringSliceVar(&repos, "repo", nil, "repo to work on (owner/name or GitHub URL); repeatable (up to 5)")
	fl.StringVar(&branch, "branch", "", "branch to start from, for every --repo")
	fl.BoolVar(&chat, "chat", false, "start a conversation with no repo and no sandbox")
	fl.StringVar(&mode, "mode", "", "auto|build|plan|ask (default auto; ask is read-only)")
	fl.StringVar(&model, "model", "", "model for the first run, one the workspace allows")
	fl.StringVar(&visibility, "visibility", "", "private|team|workspace")
	fl.StringVar(&team, "team", "", "team id, with --visibility team")
	fl.StringVar(&title, "title", "", "session title")
	fl.StringVar(&key, "idempotency-key", "", "key that makes retries safe (default: a random one)")
	fl.BoolVar(&wait, "wait", false, "wait for the first run to end")
	fl.BoolVar(&follow, "follow", false, "stream the first run until it ends")
	fl.DurationVar(&waitTimeout, "wait-timeout", 30*time.Minute, "give up waiting after this long (exit 8)")
	return c
}

// resolveRepos normalizes --repo values, or takes the origin remote's repo with a one-line note.
func (a *app) resolveRepos(flags []string) ([]string, error) {
	var out []string
	for _, r := range flags {
		n, ok := ParseRepo(r)
		if !ok {
			return nil, exitcode.Usagef("%q is not a repo: use owner/name or a GitHub URL", r)
		}
		out = append(out, n)
	}
	if len(out) > 5 {
		return nil, exitcode.Usagef("a session can work on up to 5 repos")
	}
	if len(out) > 0 {
		return out, nil
	}
	if a.GitOrigin != nil {
		if n, ok := ParseRepo(a.GitOrigin()); ok {
			fmt.Fprintf(a.Err, "Using %s from this checkout's git remote.\n", n)
			return []string{n}, nil
		}
	}
	return nil, exitcode.Usagef("no repo to work on").
		WithHint("Pass --repo owner/name, run inside a checkout of a GitHub repo, or use --chat.")
}

type startResult struct {
	ID      string       `json:"id"`
	URL     string       `json:"url"`
	Outcome string       `json:"outcome,omitempty"`
	Summary string       `json:"summary,omitempty"`
	Session *api.Session `json:"session,omitempty"`
}

func (a *app) writeStart(r *startResult, waited bool) error {
	if a.useJSON() {
		return output.JSON(a.Out, r)
	}
	fmt.Fprintln(a.Out, r.ID)
	fmt.Fprintln(a.Out, r.URL)
	if waited && r.Outcome != "" {
		fmt.Fprintf(a.Out, "Run %s: %s\n", r.Outcome, r.Summary)
	}
	if r.Session != nil && r.Session.PullRequest != nil {
		fmt.Fprintf(a.Out, "Pull request: %s\n", r.Session.PullRequest.URL)
	}
	return nil
}

// waitForRun polls the session's events until a run ends. It returns a typed error for the exit
// codes: 10 when the run failed or was stopped, 8 on timeout.
func (a *app) waitForRun(ctx context.Context, rt *conn, r *startResult, timeout time.Duration) error {
	deadline := a.Now().Add(timeout)
	since := 0
	for {
		events, err := rt.Client.Events(ctx, r.ID, since)
		if err != nil {
			return err
		}
		for _, ev := range events {
			if ev.Seq > since {
				since = ev.Seq
			}
			if ev.Type != "run.completed" {
				continue
			}
			var rc api.RunCompleted
			if err := json.Unmarshal(ev.Payload, &rc); err != nil {
				return exitcode.New(exitcode.General, "bad_response", "couldn't read the run's result")
			}
			r.Outcome, r.Summary = rc.Outcome, rc.Summary
			if rc.Outcome == "failed" || rc.Outcome == "stopped" {
				return exitcode.New(exitcode.RunFailed, "run_"+rc.Outcome, "The run ended "+rc.Outcome+".").
					WithHint(rc.Summary)
			}
			return nil
		}
		if timeout > 0 && !a.Now().Before(deadline) {
			return exitcode.New(exitcode.Timeout, "wait_timeout", "Timed out waiting for the run to end.").
				WithHint("The session keeps running: " + r.URL)
		}
		if err := a.Sleep(ctx, a.PollInterval); err != nil {
			return err
		}
	}
}

// ---- stop ----

type stopResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	exit int
}

func (a *app) sessionsStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id>...",
		Short: "Stop the running work of one or more sessions",
		Long: `Stop each session's current run. What the run changed is kept. Give several ids, or "-" to
read ids from stdin, one per line. Sessions are stopped four at a time, each result is reported,
and the exit code is 9 when only some of them could be stopped.`,
		Example: "  waniis sessions stop ses_01abc ses_01def\n  waniis sessions list --status running --json | jq -r '.items[].id' | waniis sessions stop -",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := a.collectIDs(args)
			if err != nil {
				return err
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			results := a.runBulk(cmd.Context(), ids, rt.Client.StopRun)
			return a.reportBulk(results, "stopped")
		},
	}
}

func (a *app) sessionsPRCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr <id>...",
		Short: "Open a pull request for the changes a session kept",
		Long: `A run opens a pull request only when someone asks for one; otherwise its verified changes are
kept in the session, ready. This opens the pull request for them, as "Open pull request" does in
the web app. Give several ids, or "-" to read ids from stdin, one per line.`,
		Example: "  waniis sessions pr ses_01abc",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := a.collectIDs(args)
			if err != nil {
				return err
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			results := a.runBulk(cmd.Context(), ids, rt.Client.PublishChanges)
			return a.reportBulk(results, "asked to open a pull request")
		},
	}
}

// collectIDs expands "-" to the ids on stdin and removes duplicates, keeping order.
func (a *app) collectIDs(args []string) ([]string, error) {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, arg := range args {
		if arg != "-" {
			add(arg)
			continue
		}
		sc := bufio.NewScanner(a.In)
		for sc.Scan() {
			for _, f := range strings.Fields(sc.Text()) {
				add(f)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, exitcode.Usagef("no session ids given")
	}
	return ids, nil
}

const bulkConcurrency = 4

func (a *app) runBulk(ctx context.Context, ids []string, do func(context.Context, string) error) []stopResult {
	results := make([]stopResult, len(ids))
	sem := make(chan struct{}, bulkConcurrency)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			res := stopResult{ID: id, OK: true}
			if err := do(ctx, id); err != nil {
				ee := exitcode.As(err)
				res.OK, res.exit = false, ee.Code
				res.Error = &struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}{ee.APICode, ee.Message}
			}
			results[i] = res
		}()
	}
	wg.Wait()
	return results
}

func (a *app) reportBulk(results []stopResult, verb string) error {
	failed := 0
	firstExit := 0
	for _, r := range results {
		if !r.OK {
			failed++
			if firstExit == 0 {
				firstExit = r.exit
			}
		}
	}
	if a.useJSON() {
		if err := output.JSON(a.Out, map[string]any{"results": results, "succeeded": len(results) - failed, "failed": failed}); err != nil {
			return err
		}
	} else {
		rows := make([][]string, 0, len(results))
		for _, r := range results {
			status, detail := verb, ""
			if !r.OK {
				status, detail = "failed", r.Error.Message
			}
			rows = append(rows, []string{r.ID, status, detail})
		}
		if err := output.Table(a.Out, []string{"ID", "RESULT", "DETAIL"}, rows); err != nil {
			return err
		}
	}
	switch {
	case failed == 0:
		return nil
	case failed == len(results):
		return exitcode.New(firstExit, "bulk_failed", fmt.Sprintf("None of the %d sessions could be %s.", len(results), verb))
	}
	return exitcode.New(exitcode.Partial, "bulk_partial", fmt.Sprintf("%d of %d sessions could not be %s.", failed, len(results), verb))
}

// ---- share ----

func (a *app) sessionsShareCmd() *cobra.Command {
	var link, role, visibility, team string
	var revoke bool
	var invite, remove []string
	c := &cobra.Command{
		Use:   "share <id>",
		Short: "Share a session: a link, people, or its visibility",
		Example: `  waniis sessions share ses_01abc --link viewer
  waniis sessions share ses_01abc --invite maya --invite sam --role collaborator
  waniis sessions share ses_01abc --revoke-link
  waniis sessions share ses_01abc --visibility workspace`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := firstErr(oneOf("link", link, validRoles), oneOf("role", role, validRoles), oneOf("visibility", visibility, validVisibility)); err != nil {
				return err
			}
			if link == "" && !revoke && len(invite) == 0 && len(remove) == 0 && visibility == "" {
				return exitcode.Usagef("nothing to do").WithHint("Use --link, --revoke-link, --invite, --remove or --visibility.")
			}
			if link != "" && revoke {
				return exitcode.Usagef("--link and --revoke-link can't be used together")
			}
			if team != "" && visibility != "team" {
				return exitcode.Usagef("--team needs --visibility team")
			}
			if visibility == "team" && team == "" {
				return exitcode.Usagef("--visibility team needs --team")
			}
			if role == "" {
				role = "viewer"
			}
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			out := map[string]any{"id": id}
			var lines []string
			if visibility != "" {
				if err := rt.Client.SetVisibility(ctx, id, visibility, team); err != nil {
					return err
				}
				out["visibility"] = visibility
				lines = append(lines, "Visibility set to "+visibility)
			}
			if link != "" {
				l, err := rt.Client.SetShareLink(ctx, id, link)
				if err != nil {
					return err
				}
				out["link"] = l
				if l != nil {
					lines = append(lines, fmt.Sprintf("Share link (%s): %s", l.Role, l.URL))
				}
			}
			if revoke {
				if err := rt.Client.RevokeShareLink(ctx, id); err != nil {
					return err
				}
				out["linkRevoked"] = true
				lines = append(lines, "Share link revoked")
			}
			var invited []any
			for _, login := range invite {
				p, err := rt.Client.Invite(ctx, id, strings.TrimPrefix(login, "@"), role)
				if err != nil {
					return err
				}
				invited = append(invited, p)
				lines = append(lines, fmt.Sprintf("Invited %s as %s", login, role))
			}
			if invited != nil {
				out["invited"] = invited
			}
			if len(remove) > 0 {
				people, err := rt.Client.People(ctx, id)
				if err != nil {
					return err
				}
				var removed []string
				for _, login := range remove {
					uid := ""
					for _, p := range people {
						if strings.EqualFold(p.Login, strings.TrimPrefix(login, "@")) {
							uid = p.ID
						}
					}
					if uid == "" {
						return exitcode.New(exitcode.NotFound, "not_found", fmt.Sprintf("%s doesn't have access to this session.", login))
					}
					if err := rt.Client.RemovePerson(ctx, id, uid); err != nil {
						return err
					}
					removed = append(removed, login)
					lines = append(lines, "Removed "+login)
				}
				out["removed"] = removed
			}
			if a.useJSON() {
				return output.JSON(a.Out, out)
			}
			for _, l := range lines {
				fmt.Fprintln(a.Out, l)
			}
			return nil
		},
	}
	fl := c.Flags()
	fl.StringVar(&link, "link", "", "turn the share link on for viewer|collaborator and print it")
	fl.BoolVar(&revoke, "revoke-link", false, "turn the share link off")
	fl.StringSliceVar(&invite, "invite", nil, "invite a person by login; repeatable")
	fl.StringVar(&role, "role", "", "role for --invite: viewer|collaborator (default viewer)")
	fl.StringSliceVar(&remove, "remove", nil, "remove a person by login; repeatable")
	fl.StringVar(&visibility, "visibility", "", "private|team|workspace")
	fl.StringVar(&team, "team", "", "team id, with --visibility team")
	return c
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// ---- open ----

func (a *app) sessionsOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <id>",
		Short: "Open a session in your browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := a.resolveBase()
			if err != nil {
				return err
			}
			u := sessionURL(base, args[0])
			opened := false
			if a.OpenBrowser != nil {
				opened = a.OpenBrowser(u) == nil
			}
			if a.useJSON() {
				return output.JSON(a.Out, map[string]any{"id": args[0], "url": u, "opened": opened})
			}
			fmt.Fprintln(a.Out, u)
			if !opened {
				fmt.Fprintln(a.Err, "Couldn't open a browser; open the URL above yourself.")
			}
			return nil
		},
	}
}
