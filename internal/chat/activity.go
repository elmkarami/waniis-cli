package chat

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/render"
	"github.com/elmkarami/waniis-cli/internal/style"
)

// Words are what the status line says between real updates: Waniis's own gentle words.
var Words = []string{
	"Thinking",
	"Looking closer",
	"Connecting the dots",
	"Weighing options",
	"Checking twice",
	"Turning it over",
	"Following the thread",
	"Sorting it out",
	"Gathering my thoughts",
	"Piecing it together",
	"Reading between the lines",
	"Taking a careful look",
}

// SpinnerFrames are the braille frames of the spinner, drawn about ten times a second.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	// labelFresh is how long a real update stays on the status line before the words resume.
	labelFresh = 6 * time.Second
	// wordEvery is how long one of Waniis's words stays.
	wordEvery = 3 * time.Second
)

// Activity is the live status of a working session: what Waniis is doing now, from the events,
// and for how long. It is pure and driven by an injected clock, so it can be tested.
type Activity struct {
	mu      sync.Mutex
	now     func() time.Time
	active  bool
	since   time.Time
	label   string
	labelAt time.Time
	starts  int
}

// NewActivity returns an idle status.
func NewActivity(now func() time.Time) *Activity { return &Activity{now: now} }

// Start begins the status (no-op while one is going).
func (a *Activity) Start() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		a.active, a.since, a.label = true, a.now(), ""
		a.starts++
	}
}

// Stop ends the status.
func (a *Activity) Stop() {
	a.mu.Lock()
	a.active, a.label = false, ""
	a.mu.Unlock()
}

// Active reports whether the status is going.
func (a *Activity) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}

// Observe takes one event: a real update replaces what the line says.
func (a *Activity) Observe(ev api.Event) {
	text := Describe(ev)
	if text == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active {
		a.label, a.labelAt = text, a.now()
	}
}

// Elapsed is how long the status has been going.
func (a *Activity) Elapsed() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		return 0
	}
	return a.now().Sub(a.since)
}

// Text is what the status says now, without the spinner: the latest real update while it is
// fresh, otherwise one of Waniis's words, changing every few seconds, then the elapsed time.
func (a *Activity) Text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		return ""
	}
	now := a.now()
	elapsed := now.Sub(a.since)
	say := a.label
	if say == "" || now.Sub(a.labelAt) > labelFresh {
		step := int(elapsed / wordEvery)
		say = Words[(step+(a.starts-1)*5)%len(Words)] + "…"
	}
	shown := "0s"
	if elapsed >= time.Second {
		shown = render.FormatElapsed(elapsed)
	}
	return say + " · " + shown
}

// Line is the status line: a spinner frame (a static dot without a spinner) and the text.
func (a *Activity) Line(st style.Style, frame int, spinner bool) string {
	t := a.Text()
	if t == "" {
		return ""
	}
	mark := "•"
	if spinner {
		mark = SpinnerFrames[frame%len(SpinnerFrames)]
	}
	return st.Cyan(mark) + " " + st.Dim(t)
}

// Describe says what a real event means for what Waniis is doing, "" when it says nothing new.
func Describe(ev api.Event) string {
	var p map[string]any
	if json.Unmarshal(ev.Payload, &p) != nil {
		p = map[string]any{}
	}
	str := func(k string) string { v, _ := p[k].(string); return v }
	switch ev.Type {
	case "tool.call":
		args, _ := p["args"].(map[string]any)
		return describeTool(str("tool"), args)
	case "file.changed":
		if path := str("path"); path != "" {
			if str("changeType") == "deleted" {
				return "Removing " + short(path) + "…"
			}
			return "Writing " + short(path) + "…"
		}
	case "agent.message.delta":
		return "Writing the answer…"
	case "agent.retrying":
		return "The model is busy, trying again…"
	case "verification.started":
		return "Running checks…"
	case "verification.completed":
		if f, _ := p["fixing"].(bool); f {
			return "Fixing what the checks found…"
		}
	case "review.started":
		return "Reviewing the change…"
	case "changes.publish_requested":
		return "Opening a pull request…"
	case "session.working":
		return "Starting a sandbox…"
	case "session.repo_checked_out":
		if r := str("repo"); r != "" {
			return "Reading " + r + "…"
		}
	case "sandbox.status":
		switch str("status") {
		case "provisioning", "restoring":
			return "Starting a sandbox…"
		case "syncing":
			return "Setting up the sandbox…"
		}
	case "mcp.approval_requested", "network.access_requested":
		return "Waiting for your answer…"
	}
	return ""
}

var knownServers = map[string]string{
	"github": "GitHub", "gitlab": "GitLab", "sentry": "Sentry", "linear": "Linear", "slack": "Slack",
	"jira": "Jira", "atlassian": "Atlassian", "notion": "Notion", "datadog": "Datadog", "gmail": "Gmail",
	"figma": "Figma", "stripe": "Stripe", "postgres": "Postgres", "pagerduty": "PagerDuty",
}

var builtinTools = map[string]bool{
	"read": true, "write": true, "edit": true, "multiedit": true, "patch": true, "apply_patch": true,
	"bash": true, "shell": true, "grep": true, "glob": true, "list": true, "ls": true, "find": true,
	"webfetch": true, "websearch": true, "web_fetch": true, "web_search": true, "task": true,
	"todowrite": true, "todoread": true, "view": true, "fetch": true,
}

// nice writes a server name as people read it: GitHub, Sentry, Waniis browser.
func nice(server string) string {
	server = strings.TrimSpace(strings.ToLower(server))
	if n, ok := knownServers[server]; ok {
		return n
	}
	server = strings.NewReplacer("rafiiq", "waniis", "-", " ", "_", " ").Replace(server)
	if server == "" {
		return "a tool"
	}
	return strings.ToUpper(server[:1]) + server[1:]
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 48 {
		return "…" + string(r[len(r)-47:])
	}
	return s
}

func argString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func repoArg(args map[string]any) string {
	if r := argString(args, "repo", "repository", "fullName"); r != "" {
		if o := argString(args, "owner"); o != "" && !strings.Contains(r, "/") {
			return o + "/" + r
		}
		return r
	}
	return ""
}

func describeTool(tool string, args map[string]any) string {
	name := strings.ToLower(render.DisplayToolName(tool))
	server, fn := "", name
	if i := strings.Index(name, "."); i > 0 {
		server, fn = name[:i], name[i+1:]
	} else if !builtinTools[name] {
		if i := strings.Index(name, "_"); i > 0 {
			server, fn = name[:i], name[i+1:]
		}
	}
	path := argString(args, "path", "file", "filePath", "file_path")
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(fn, w) {
				return true
			}
		}
		return false
	}
	switch {
	case server == "" && has("bash", "shell", "exec"):
		return describeCommand(argString(args, "command", "cmd"))
	case server == "" && (fn == "webfetch" || fn == "web_fetch" || fn == "fetch"):
		if u, err := url.Parse(argString(args, "url")); err == nil && u.Host != "" {
			return "Fetching " + u.Host + "…"
		}
		return "Fetching a page…"
	case server == "" && (fn == "websearch" || fn == "web_search"):
		return "Searching the web…"
	case server == "" && has("grep", "glob", "find"):
		return "Searching the code…"
	case server == "" && (fn == "list" || fn == "ls"):
		return "Looking around " + orDefault(short(path), "the project") + "…"
	case server == "" && has("read", "view"):
		return "Reading " + orDefault(short(path), "the code") + "…"
	case server == "" && has("write", "edit", "patch"):
		return "Writing " + orDefault(short(path), "the change") + "…"
	case server == "" && fn == "task":
		return "Delegating a step…"
	case server == "":
		return ""
	}
	who := nice(server)
	repo := repoArg(args)
	switch {
	case has("search", "query", "find", "list"):
		return "Searching " + who + "…"
	case has("get", "read", "fetch", "view", "show", "describe"):
		return "Reading " + orDefault(orDefault(repo, short(path)), who) + "…"
	case has("create_pull", "open_pull", "pull_request", "create_pr"):
		return "Opening a pull request…"
	case has("create", "write", "update", "edit", "push", "post", "send", "comment"):
		if path != "" {
			return "Writing " + short(path) + "…"
		}
		return "Updating " + who + "…"
	}
	return "Asking " + who + "…"
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func describeCommand(cmd string) string {
	c := strings.ToLower(cmd)
	any := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(c, s) {
				return true
			}
		}
		return false
	}
	switch {
	case c == "":
		return "Running a command…"
	case any("gh pr create", "gh pr "):
		return "Opening a pull request…"
	case any("git commit"):
		return "Committing…"
	case any("git push"):
		return "Pushing…"
	case any(" test", "pytest", "vitest", "jest", "go test", "cargo test", "rspec", "phpunit"):
		return "Running tests…"
	case any("lint", "eslint", "golangci", "ruff", "prettier", "gofmt"):
		return "Linting…"
	case any("tsc", "typecheck", "mypy", "go vet"):
		return "Type-checking…"
	case any("install", "npm ci", "go mod", "uv sync", "pip "):
		return "Installing dependencies…"
	case any("build", "compile", "make"):
		return "Building…"
	}
	words := strings.Fields(cmd)
	if len(words) > 3 {
		words = words[:3]
	}
	return "Running " + short(fmt.Sprint(strings.Join(words, " "))) + "…"
}
