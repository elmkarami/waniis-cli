// Package commands holds the Cobra commands. Each command parses flags, calls the API client
// through the runtime, and renders; nothing here talks HTTP or the terminal directly.
package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/auth"
	"github.com/elmkarami/waniis-cli/internal/config"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

// Build information, set by the linker (GoReleaser).
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Deps is everything the commands need from the outside world, so tests can replace it.
type Deps struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Getenv func(string) string
	HTTP   *http.Client
	Store  auth.Store
	// OpenBrowser opens a URL; it returns an error when it can't.
	OpenBrowser func(url string) error
	Sleep       func(context.Context, time.Duration) error
	Now         func() time.Time
	NewKey      func() string
	// StdinTTY and StdoutTTY say whether the streams are terminals.
	StdinTTY, StdoutTTY bool
	// GitOrigin returns the `origin` remote URL of the current checkout, or "".
	GitOrigin func() string
	Hostname  func() string
	// PollInterval is how often --wait checks the session.
	PollInterval time.Duration
	// TermWidth is the terminal's width in columns; nil or 0 means 80.
	TermWidth func() int
	// RetryDelay overrides the API client's base retry delay when non-zero.
	RetryDelay time.Duration
}

// DefaultDeps wires the real process.
func DefaultDeps(store auth.Store) Deps {
	return Deps{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Getenv: os.Getenv, HTTP: &http.Client{}, Store: store,
		Sleep: api.SleepContext, Now: time.Now, NewKey: api.NewUUID,
		GitOrigin: func() string {
			out, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		},
		Hostname:     func() string { h, _ := os.Hostname(); return h },
		PollInterval: 3 * time.Second,
	}
}

// globals are the flags every command shares.
type globals struct {
	JSON      bool
	Workspace string
	APIURL    string
	NoColor   bool
	Verbose   bool
	Timeout   time.Duration
	Charts    string
}

// app is the state shared by a command tree: the dependencies and the parsed global flags.
type app struct {
	Deps
	g       globals
	started bool // set once a command's own code starts, to tell usage errors from run errors
}

// New builds the root command.
func New(d Deps) *cobra.Command {
	root, _ := build(d)
	return root
}

func build(d Deps) (*cobra.Command, *app) {
	a := &app{Deps: d}
	root := &cobra.Command{
		Use:   "waniis",
		Short: "Waniis from the command line",
		Long: `waniis starts, follows, stops, shares and searches Waniis sessions from a terminal,
and exports them to CSV or Excel. It talks to the Waniis API as you, with a token from the
browser sign-in (waniis login) or a personal token (WANIIS_TOKEN).`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			a.started = true
			return oneOf("charts", a.g.Charts, []string{"auto", "always", "never"})
		},
	}
	root.SetOut(d.Out)
	root.SetErr(d.Err)
	root.SetIn(d.In)
	pf := root.PersistentFlags()
	pf.BoolVar(&a.g.JSON, "json", false, "machine-readable JSON output")
	pf.StringVar(&a.g.Workspace, "workspace", "", "workspace id (env WANIIS_WORKSPACE)")
	pf.StringVar(&a.g.APIURL, "api-url", "", "Waniis URL (env WANIIS_API_URL, default "+config.DefaultAPIURL+")")
	pf.BoolVar(&a.g.NoColor, "no-color", false, "disable colored output (also NO_COLOR)")
	pf.BoolVar(&a.g.Verbose, "verbose", false, "log requests to stderr (tokens are redacted)")
	pf.DurationVar(&a.g.Timeout, "timeout", 30*time.Second, "timeout for each API request")
	pf.StringVar(&a.g.Charts, "charts", "auto", "draw charts in answers: auto (on a terminal), always or never; a table is always shown")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return exitcode.Usagef("%s", err.Error()).WithHint("Run the command with --help for its usage.")
	})
	root.AddCommand(
		a.loginCmd(), a.logoutCmd(), a.whoamiCmd(),
		a.sessionsCmd(), a.exportCmd(), a.versionCmd(),
	)
	// The most used session commands also at the top level (CLT-15/FR-8).
	root.AddCommand(
		topLevel(a.sessionsStartCmd(), "new \"<prompt>\""),
		topLevel(a.sessionsListCmd(), "ls"),
		topLevel(a.sessionsLogsCmd(), "logs <id>"),
		a.watchCmd(),
	)
	root.AddCommand(a.authCmd())
	root.InitDefaultCompletionCmd()
	return root, a
}

// Execute runs the command tree with args and returns the process exit code. Errors are written
// to stderr here: text for people, a JSON object with --json.
func Execute(ctx context.Context, d Deps, args []string) int {
	root, a := build(d)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitcode.OK
	}
	return a.reportError(ctx, args, err)
}

// runtime is a resolved API client for one command run.
type conn struct {
	*app
	Client  *api.Client
	Base    string
	Source  *auth.Source
	Context context.Context
}

// useJSON reports whether output is JSON: with --json.
func (a *app) useJSON() bool { return a.g.JSON }

// resolveBase returns the validated API base URL (flag, env, default).
func (a *app) resolveBase() (string, error) {
	return config.NormalizeBaseURL(config.Pick(a.g.APIURL, a.Getenv(config.EnvAPIURL), config.DefaultAPIURL))
}

// newClient builds an API client with the token source attached when authenticated is true.
func (a *app) newClient(authenticated bool) (*conn, error) {
	base, err := a.resolveBase()
	if err != nil {
		return nil, err
	}
	c := api.New(base, a.HTTP)
	c.Timeout = a.g.Timeout
	c.UserAgent = fmt.Sprintf("waniis-cli/%s (%s; %s)", Version, goos(), goarch())
	c.Workspace = config.Pick(a.g.Workspace, a.Getenv(config.EnvWorkspace), "")
	c.Sleep = a.Sleep
	c.NewKey = a.NewKey
	if a.RetryDelay > 0 {
		c.BaseDelay = a.RetryDelay
	}
	if a.g.Verbose {
		c.Log = a.Err
	}
	rt := &conn{app: a, Client: c, Base: base}
	if authenticated {
		src := &auth.Source{Host: base, EnvTok: strings.TrimSpace(a.Getenv(config.EnvToken)), Store: a.Store, Now: a.Now}
		// The refresher uses its own anonymous client so it can't recurse into this one.
		rc := *c
		rc.Tokens = nil
		src.Renew = auth.NewRefresher(&rc, a.Now)
		c.Tokens = src
		rt.Source = src
	}
	return rt, nil
}

func (a *app) interactive() bool { return a.StdinTTY && a.StdoutTTY && a.Getenv("CI") == "" }

// topLevel turns a sessions subcommand into a top-level shortcut.
func topLevel(c *cobra.Command, use string) *cobra.Command {
	c.Use = use
	c.Aliases = nil
	return c
}
