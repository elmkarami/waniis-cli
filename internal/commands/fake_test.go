package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/auth"
)

const (
	testPAT    = "waniis_pat_abcdef123456"
	testAccess = "waniis_cli_access123456"
	testRef    = "waniis_cli_refresh123456"
)

// recorded is one request the fake server saw.
type recorded struct {
	Method, Path string
	Query        map[string][]string
	Header       http.Header
	Body         map[string]any
}

// fake implements the CLI spec's API contract over httptest.
type fake struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	reqs []recorded

	// device flow: the answers to successive polls
	devicePolls   []string // "pending", "slow_down", "denied", "expired", "ok"
	pollIdx       int
	revoked       []string
	validToken    string // the bearer the API accepts
	me            map[string]any
	accessPending bool

	sessions       []map[string]any // all sessions, newest first
	pageSize       int              // server-side page cap (cursor = offset)
	interpreted    map[string]any
	failCreateWith []int             // statuses to answer successive creates with before succeeding
	dropCreate     int               // connection drops before succeeding
	created        map[string]string // idempotency key -> session id
	createCount    int
	events         map[string][]map[string]any
	missing        map[string]bool // session ids that 404
	people         []map[string]any

	// watching and steering
	ws               map[string][]wsScript
	conns            []wsConn
	status           map[string]map[string]any
	legacyEvents     bool
	commands         []recorded
	cmdReplies       map[string]map[string]any
	promptSeq        int
	noRun            bool
	running          map[string]bool
	networkForbidden bool
	failCommands     []int
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, validToken: testPAT, created: map[string]string{}, events: map[string][]map[string]any{}, ws: map[string][]wsScript{}, status: map[string]map[string]any{},
		cmdReplies: map[string]map[string]any{}, running: map[string]bool{}, missing: map[string]bool{},
		me: map[string]any{"id": "usr_1", "login": "maya", "name": "Maya L",
			"workspaces": []map[string]any{{"id": "ws_1", "name": "Maya L", "kind": "personal"}}}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func session(id, title, status string, pr int, repo string, minutesAgo int) map[string]any {
	s := map[string]any{"id": id, "title": title, "status": status, "stage": "working",
		"repos":        []map[string]any{{"fullName": repo, "cloneUrl": "https://github.com/" + repo + ".git"}},
		"pullRequest":  nil,
		"createdAt":    "2026-11-10T10:00:00Z",
		"lastActiveAt": time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Add(-time.Duration(minutesAgo) * time.Minute).Format(time.RFC3339),
		"visibility":   "private", "owner": map[string]any{"id": "usr_1", "login": "maya", "name": "Maya L"},
		"extraServerField": "kept"}
	if pr > 0 {
		s["pullRequest"] = map[string]any{"number": pr, "url": fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr)}
	}
	return s
}

func (f *fake) count(method, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

func (f *fake) last(method, path string) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		if f.reqs[i].Method == method && f.reqs[i].Path == path {
			return f.reqs[i]
		}
	}
	f.t.Fatalf("no %s %s request was made", method, path)
	return recorded{}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-request-id", "req_test")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), body})
	f.mu.Unlock()
	p := r.URL.Path
	m := r.Method

	// --- device flow (no bearer)
	switch {
	case m == "POST" && p == "/auth/device/code":
		writeJSON(w, 200, map[string]any{"device_code": "dev_code_1", "user_code": "WDJB-MJHT",
			"verification_uri": "https://app.waniis.com/device", "verification_uri_complete": "https://app.waniis.com/device?code=WDJB-MJHT",
			"expires_in": 600, "interval": 5})
		return
	case m == "POST" && p == "/auth/device/token":
		f.mu.Lock()
		if body["grant_type"] == "refresh_token" {
			f.mu.Unlock()
			if body["refresh_token"] != testRef {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			f.mu.Lock()
			f.validToken = "waniis_cli_rotated"
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"access_token": "waniis_cli_rotated", "refresh_token": "waniis_cli_refresh_rotated", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		step := "ok"
		if f.pollIdx < len(f.devicePolls) {
			step = f.devicePolls[f.pollIdx]
		}
		f.pollIdx++
		f.mu.Unlock()
		switch step {
		case "pending":
			writeJSON(w, 400, map[string]any{"error": "authorization_pending"})
		case "slow_down":
			writeJSON(w, 400, map[string]any{"error": "slow_down"})
		case "denied":
			writeJSON(w, 400, map[string]any{"error": "access_denied"})
		case "expired":
			writeJSON(w, 400, map[string]any{"error": "expired_token"})
		default:
			f.mu.Lock()
			f.validToken = testAccess
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"access_token": testAccess, "refresh_token": testRef, "token_type": "Bearer", "expires_in": 3600, "scope": "read sessions"})
		}
		return
	case m == "POST" && p == "/auth/device/revoke":
		f.mu.Lock()
		f.revoked = append(f.revoked, fmt.Sprint(body["token"]))
		f.mu.Unlock()
		w.WriteHeader(204)
		return
	}

	// --- /v1 needs the bearer
	f.mu.Lock()
	want, pending := f.validToken, f.accessPending
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+want {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if pending && p != "/v1/me" {
		writeJSON(w, 403, map[string]any{"error": "access_pending", "message": "Your request for access hasn't been approved yet."})
		return
	}
	switch {
	case m == "GET" && p == "/v1/me":
		writeJSON(w, 200, f.me)
	case m == "GET" && p == "/v1/models":
		writeJSON(w, 200, fakeModels)
	case m == "GET" && (p == "/v1/sessions" || p == "/v1/sessions/search"):
		f.listSessions(w, r, p == "/v1/sessions/search")
	case m == "POST" && p == "/v1/sessions":
		f.createSession(w, r, body)
	case strings.HasPrefix(p, "/v1/sessions/"):
		f.sessionRoute(w, r, body)
	default:
		writeJSON(w, 404, map[string]any{"error": "not_found"})
	}
}

func (f *fake) listSessions(w http.ResponseWriter, r *http.Request, search bool) {
	q := r.URL.Query()
	off, _ := strconv.Atoi(q.Get("cursor"))
	size := f.pageSize
	if size == 0 {
		size = 100
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l < size {
		size = l
	}
	end := off + size
	if end > len(f.sessions) {
		end = len(f.sessions)
	}
	out := map[string]any{"items": f.sessions[off:end], "next": nil}
	if end < len(f.sessions) {
		out["next"] = strconv.Itoa(end)
	}
	if search && f.interpreted != nil && q.Get("interpret") != "false" {
		out["interpreted"] = f.interpreted
	}
	writeJSON(w, 200, out)
}

func (f *fake) createSession(w http.ResponseWriter, r *http.Request, body map[string]any) {
	key := r.Header.Get("Idempotency-Key")
	f.mu.Lock()
	if f.dropCreate > 0 {
		f.dropCreate--
		f.mu.Unlock()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
		return
	}
	if len(f.failCreateWith) > 0 {
		st := f.failCreateWith[0]
		f.failCreateWith = f.failCreateWith[1:]
		f.mu.Unlock()
		writeJSON(w, st, map[string]any{"error": "unavailable"})
		return
	}
	id, seen := f.created[key]
	if !seen {
		f.createCount++
		id = fmt.Sprintf("ses_%02d", f.createCount)
		if key != "" {
			f.created[key] = id
		}
	}
	f.mu.Unlock()
	writeJSON(w, 201, map[string]any{"id": id, "events": []any{}})
}

func (f *fake) sessionRoute(w http.ResponseWriter, r *http.Request, body map[string]any) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.SplitN(rest, "/", 3)
	id := parts[0]
	if f.missing[id] {
		writeJSON(w, 404, map[string]any{"error": "not_found"})
		return
	}
	sub := ""
	if len(parts) > 1 {
		sub = strings.Join(parts[1:], "/")
	}
	if f.extraRoute(w, r, id, sub, body) {
		return
	}
	switch {
	case r.Method == "GET" && sub == "":
		for _, s := range f.sessions {
			if s["id"] == id {
				writeJSON(w, 200, s)
				return
			}
		}
		writeJSON(w, 200, session(id, "created", "idle", 0, "acme/api", 1))
	case r.Method == "GET" && sub == "events":
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		var out []map[string]any
		for _, e := range f.events[id] {
			if e["seq"].(int) > since {
				out = append(out, e)
			}
		}
		writeJSON(w, 200, map[string]any{"events": out})
	case r.Method == "POST" && sub == "commands":
		if body["type"] != "run.stop" && body["type"] != "changes.publish" {
			writeJSON(w, 400, map[string]any{"error": "invalid_command"})
			return
		}
		writeJSON(w, 200, map[string]any{"events": []any{}})
	case r.Method == "PUT" && sub == "share-link":
		writeJSON(w, 200, map[string]any{"link": map[string]any{"role": body["role"], "url": "https://app.waniis.com/join/tok123", "createdAt": "2026-11-20T00:00:00Z"}})
	case r.Method == "DELETE" && sub == "share-link":
		writeJSON(w, 200, map[string]any{"ok": true})
	case r.Method == "GET" && sub == "people":
		writeJSON(w, 200, map[string]any{"people": f.people, "role": "owner"})
	case r.Method == "POST" && sub == "people":
		writeJSON(w, 201, map[string]any{"person": map[string]any{"id": "usr_9", "login": body["login"], "role": body["role"]}})
	case r.Method == "DELETE" && strings.HasPrefix(sub, "people/"):
		w.WriteHeader(204)
	case r.Method == "PUT" && sub == "visibility":
		writeJSON(w, 200, map[string]any{"visibility": body["visibility"]})
	default:
		writeJSON(w, 404, map[string]any{"error": "not_found"})
	}
}

// harness runs the CLI against the fake.
type harness struct {
	t      *testing.T
	f      *fake
	store  *auth.MemoryStore
	env    map[string]string
	in     string
	tty    bool
	sleeps []time.Duration
	opened []string
	git    string
	now    time.Time
	keys   int
	keyMu  sync.Mutex
}

func (h *harness) newKey() string {
	h.keyMu.Lock()
	defer h.keyMu.Unlock()
	h.keys++
	return "key-" + strconv.Itoa(h.keys)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := newFake(t)
	h := &harness{t: t, f: f, store: &auth.MemoryStore{}, env: map[string]string{}, now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	return h
}

// signIn puts a personal token in the credential store for the fake's host.
func (h *harness) signIn() {
	_ = h.store.Save(&auth.Credentials{Host: h.f.srv.URL, Token: testPAT, Kind: auth.KindPersonal, UserID: "usr_1", Login: "maya"})
}

func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	d := Deps{
		In: strings.NewReader(h.in), Out: &out, Err: &errb,
		Getenv: func(k string) string { return h.env[k] },
		HTTP:   h.f.srv.Client(), Store: h.store,
		OpenBrowser: func(u string) error { h.opened = append(h.opened, u); return nil },
		Sleep: func(ctx context.Context, d time.Duration) error {
			h.sleeps = append(h.sleeps, d)
			h.now = h.now.Add(d)
			return ctx.Err()
		},
		Now:      func() time.Time { return h.now },
		NewKey:   h.newKey,
		StdinTTY: h.tty, StdoutTTY: h.tty,
		GitOrigin:    func() string { return h.git },
		Hostname:     func() string { return "maya-mbp" },
		PollInterval: 2 * time.Second, RetryDelay: time.Millisecond,
	}
	full := append([]string{"--api-url", h.f.srv.URL}, args...)
	code = Execute(context.Background(), d, full)
	return code, out.String(), errb.String()
}

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return m
}

func eq[T comparable](t *testing.T, got, want T, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("expected %q in:\n%s", sub, s)
	}
}

func notContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("did not expect %q in:\n%s", sub, s)
	}
}

// runWithHook is run with a function called on every sleep (poll), to change the fake's state.
func (h *harness) runWithHook(hook func(), args ...string) (int, string, string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	d := Deps{
		In: strings.NewReader(h.in), Out: &out, Err: &errb,
		Getenv: func(k string) string { return h.env[k] },
		HTTP:   h.f.srv.Client(), Store: h.store,
		Sleep: func(ctx context.Context, d time.Duration) error {
			h.now = h.now.Add(d)
			hook()
			return ctx.Err()
		},
		Now:       func() time.Time { return h.now },
		NewKey:    func() string { h.keys++; return "key-" + strconv.Itoa(h.keys) },
		GitOrigin: func() string { return h.git }, Hostname: func() string { return "h" },
		PollInterval: 2 * time.Second, RetryDelay: time.Millisecond,
	}
	code := Execute(context.Background(), d, append([]string{"--api-url", h.f.srv.URL}, args...))
	return code, out.String(), errb.String()
}

// fakeModels is a workspace with an Anthropic key and no OpenAI key.
var fakeModels = map[string]any{
	"providers": []any{
		map[string]any{"id": "anthropic", "title": "Anthropic", "managed": false, "key": map[string]any{"last4": "abcd"}},
		map[string]any{"id": "openai", "title": "OpenAI", "managed": false, "key": nil},
	},
	"models": []any{
		map[string]any{"ref": "anthropic/claude-x", "provider": "anthropic", "title": "Claude X", "tier": "frontier", "available": true},
		map[string]any{"ref": "anthropic/claude-y", "provider": "anthropic", "title": "Claude Y", "tier": "fast", "available": true},
		map[string]any{"ref": "openai/gpt-z", "provider": "openai", "title": "GPT Z", "tier": "frontier", "available": false},
	},
	"routes":    map[string]any{"agent": map[string]any{"model": "anthropic/claude-x", "fallbacks": []any{}}},
	"canManage": true,
}
