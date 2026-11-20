package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// wsScript is what the fake does with one WebSocket connection, in order of connections.
type wsScript struct {
	status int              // reject the upgrade with this HTTP status
	events []map[string]any // sent (those after `since`) as frames
	frames []string         // raw frames sent before the events (presence and the like)
	hold   bool             // keep the connection open until the client goes
	then   []map[string]any // sent after a short pause, on a held connection
}

type wsConn struct {
	Since int
	Auth  string
	Query map[string][]string
}

func ev(seq int, typ string, payload map[string]any) map[string]any {
	return map[string]any{"v": 1, "seq": seq, "type": typ, "at": fmt.Sprintf("2026-11-20T10:00:%02dZ", seq%60),
		"actor": map[string]any{"kind": "system"}, "payload": payload}
}

func (f *fake) addEvents(id string, evs ...map[string]any) {
	f.mu.Lock()
	f.events[id] = append(f.events[id], evs...)
	f.mu.Unlock()
}

func (f *fake) wsConns() []wsConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wsConn(nil), f.conns...)
}

// extraRoute serves the routes added for watching and steering; it reports whether it handled one.
func (f *fake) extraRoute(w http.ResponseWriter, r *http.Request, id, sub string, body map[string]any) bool {
	switch {
	case r.Method == "GET" && sub == "events":
		if f.badEventsQuery(w, r) {
			return true
		}
		f.serveEvents(w, r, id)
	case r.Method == "GET" && sub == "stream":
		f.serveStream(w, r, id)
	case r.Method == "GET" && sub == "status":
		f.mu.Lock()
		st := f.status[id]
		f.mu.Unlock()
		if st == nil {
			writeJSON(w, 404, map[string]any{"error": "not_found"})
			return true
		}
		writeJSON(w, 200, st)
	case r.Method == "POST" && sub == "commands":
		f.serveCommand(w, r, id, body)
	case r.Method == "POST" && sub == "network/access":
		f.mu.Lock()
		deny := f.networkForbidden
		f.mu.Unlock()
		if deny {
			writeJSON(w, 403, map[string]any{"error": "forbidden", "message": "Only the workspace's owners and admins can allow a host."})
			return true
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case r.Method == "GET" && sub == "surfaces":
		writeJSON(w, 200, map[string]any{"sandbox": "ready", "previews": true, "terminals": []any{}, "editor": map[string]any{"running": false}, "desktop": map[string]any{"running": false},
			"ports": []map[string]any{{"port": 3000, "label": "web", "service": nil, "declared": true, "listening": true}}})
	case r.Method == "GET" && strings.HasPrefix(sub, "surfaces/") && strings.HasSuffix(sub, "/open"):
		w.Header().Set("Location", "https://p3000-abc.waniis.app/__handshake?ticket=T1")
		w.WriteHeader(302)
	default:
		return false
	}
	return true
}

func (f *fake) serveEvents(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	f.mu.Lock()
	all := append([]map[string]any(nil), f.events[id]...)
	legacy := f.legacyEvents
	f.mu.Unlock()
	if legacy { // the older answer: everything since `since`, only {events}
		since, _ := strconv.Atoi(q.Get("since"))
		var out []map[string]any
		for _, e := range all {
			if e["seq"].(int) > since {
				out = append(out, e)
			}
		}
		writeJSON(w, 200, map[string]any{"events": out})
		return
	}
	lastSeq := 0
	if len(all) > 0 {
		lastSeq = all[len(all)-1]["seq"].(int)
	}
	var out []map[string]any
	hasMore := false
	if t, err := strconv.Atoi(q.Get("tail")); err == nil && t > 0 {
		start := max(0, len(all)-t)
		out = all[start:]
		hasMore = start > 0
	} else {
		since, _ := strconv.Atoi(q.Get("since"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		for _, e := range all {
			if e["seq"].(int) > since {
				if limit > 0 && len(out) == limit {
					hasMore = true
					break
				}
				out = append(out, e)
			}
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, 200, map[string]any{"events": out, "lastSeq": lastSeq, "hasMore": hasMore})
}

func (f *fake) serveStream(w http.ResponseWriter, r *http.Request, id string) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	f.mu.Lock()
	f.conns = append(f.conns, wsConn{Since: since, Auth: r.Header.Get("Authorization"), Query: r.URL.Query()})
	var sc wsScript
	if len(f.ws[id]) > 0 {
		sc, f.ws[id] = f.ws[id][0], f.ws[id][1:]
	} else {
		sc = wsScript{hold: true}
	}
	f.mu.Unlock()
	if sc.status != 0 {
		writeJSON(w, sc.status, map[string]any{"error": "nope"})
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx := r.Context()
	for _, fr := range sc.frames {
		_ = c.Write(ctx, websocket.MessageText, []byte(fr))
	}
	send := func(evs []map[string]any) {
		for _, e := range evs {
			if e["seq"].(int) > since {
				b, _ := jsonMarshal(e)
				_ = c.Write(ctx, websocket.MessageText, b)
			}
		}
	}
	send(sc.events)
	if !sc.hold {
		c.CloseNow() // an abrupt drop
		return
	}
	if len(sc.then) > 0 {
		time.Sleep(20 * time.Millisecond)
		send(sc.then)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for {
		if _, _, err := c.Read(cctx); err != nil {
			return
		}
	}
}

func (f *fake) serveCommand(w http.ResponseWriter, r *http.Request, id string, body map[string]any) {
	key := r.Header.Get("Idempotency-Key")
	f.mu.Lock()
	f.commands = append(f.commands, recorded{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), body})
	if key != "" {
		if prev, ok := f.cmdReplies[key]; ok {
			f.mu.Unlock()
			w.Header().Set("Idempotent-Replayed", "true")
			writeJSON(w, 200, prev)
			return
		}
	}
	steerErr := f.noRun
	if len(f.failCommands) > 0 {
		st := f.failCommands[0]
		f.failCommands = f.failCommands[1:]
		f.mu.Unlock()
		writeJSON(w, st, map[string]any{"error": "unavailable"})
		return
	}
	f.mu.Unlock()
	var reply map[string]any
	switch body["type"] {
	case "prompt.submit":
		f.mu.Lock()
		f.promptSeq++
		n := f.promptSeq
		f.mu.Unlock()
		pid := fmt.Sprintf("prm_%d", n)
		pos := 0
		if f.isRunning(id) {
			pos = 1
		}
		reply = map[string]any{"promptId": pid, "events": []any{ev(100+n, "prompt.queued", map[string]any{"promptId": pid, "authorId": "usr_1", "text": body["text"], "mode": "build", "position": pos})}}
	case "run.steer":
		if steerErr {
			writeJSON(w, 409, map[string]any{"error": "invalid_state", "message": "No run to steer."})
			return
		}
		reply = map[string]any{"runId": "run_7", "events": []any{}}
	case "run.stop", "tool.approval.decide", "session.start_working":
		reply = map[string]any{"events": []any{}}
	default:
		writeJSON(w, 400, map[string]any{"error": "invalid_command"})
		return
	}
	f.mu.Lock()
	if key != "" {
		f.cmdReplies[key] = reply
	}
	f.mu.Unlock()
	writeJSON(w, 200, reply)
}

func (f *fake) isRunning(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[id]
}

// commandsOf returns the bodies of the commands received, by type.
func (f *fake) commandsOf(typ string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.commands {
		if c.Body["type"] == typ {
			out = append(out, c.Body)
		}
	}
	return out
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// badEventsQuery answers the server's 400 invalid_query cases.
func (f *fake) badEventsQuery(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	bad := false
	for _, k := range []string{"tail", "since", "limit", "before"} {
		if v := q.Get(k); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				bad = true
			}
		}
	}
	if q.Get("tail") != "" && (q.Get("since") != "" || q.Get("before") != "" || q.Get("limit") != "") {
		bad = true
	}
	if t, _ := strconv.Atoi(q.Get("tail")); t > 1000 {
		bad = true
	}
	if l, _ := strconv.Atoi(q.Get("limit")); l > 1000 {
		bad = true
	}
	if q.Get("since") != "" && q.Get("before") != "" {
		bad = true
	}
	if bad {
		writeJSON(w, 400, map[string]any{"error": "invalid_query"})
	}
	return bad
}
