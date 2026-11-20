// Package stream follows a session's events live: the WebSocket with the bearer token, resumed
// from the last sequence number after a drop, with a fall back to polling the events endpoint
// when WebSockets are blocked.
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/elmkarami/waniis-cli/internal/api"
)

// Options tune a Follow.
type Options struct {
	// Since is the last sequence number already seen; only later events are delivered.
	Since int
	// Backoff is the wait before reconnect attempt n (0-based). Default 0.5 s doubling to 15 s.
	Backoff func(n int) time.Duration
	// Sleep waits d or until ctx ends. Default api.SleepContext.
	Sleep func(context.Context, time.Duration) error
	// MaxDialFailures is how many connections in a row may fail before polling takes over.
	MaxDialFailures int
	// PollInterval is the polling fall back's period. Default 3 s.
	PollInterval time.Duration
	// Notice reports reconnects and the fall back (one line, no newline).
	Notice func(string)
	// PingEvery keeps an idle connection honest. Zero: 25 s.
	PingEvery time.Duration
}

// ErrStop is returned by a handler to end the follow cleanly.
var ErrStop = errors.New("stop following")

func (o *Options) defaults() {
	if o.Backoff == nil {
		o.Backoff = func(n int) time.Duration {
			d := 500 * time.Millisecond << min(n, 5)
			return min(d, 15*time.Second)
		}
	}
	if o.Sleep == nil {
		o.Sleep = api.SleepContext
	}
	if o.MaxDialFailures <= 0 {
		o.MaxDialFailures = 3
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 3 * time.Second
	}
	if o.PingEvery <= 0 {
		o.PingEvery = 25 * time.Second
	}
	if o.Notice == nil {
		o.Notice = func(string) {}
	}
}

// Follow delivers every event after o.Since to handle, in order, until handle returns an error
// (ErrStop ends it cleanly: Follow then returns nil), ctx ends, or a failure that retrying can't
// fix (not signed in, no access) happens.
func Follow(ctx context.Context, c *api.Client, id string, o Options, handle func(api.Event) error) error {
	o.defaults()
	last := o.Since
	deliver := func(ev api.Event) error {
		if ev.Seq <= last {
			return nil
		}
		last = ev.Seq
		return handle(ev)
	}
	failures, attempt := 0, 0
	for {
		got, err := runSocket(ctx, c, id, &last, o, deliver)
		switch {
		case errors.Is(err, ErrStop):
			return nil
		case err == nil || ctx.Err() == nil && !isFatal(err):
		default:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var f fatalError
			if errors.As(err, &f) {
				return f.err
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if got {
			failures, attempt = 0, 0
		} else {
			failures++
		}
		if failures >= o.MaxDialFailures {
			o.Notice("WebSocket unavailable; polling for events")
			return poll(ctx, c, id, &last, o, deliver)
		}
		o.Notice("connection lost; reconnecting")
		if err := o.Sleep(ctx, o.Backoff(attempt)); err != nil {
			return err
		}
		attempt++
	}
}

type fatalError struct{ err error }

func (f fatalError) Error() string { return f.err.Error() }
func (f fatalError) Unwrap() error { return f.err }

func isFatal(err error) bool { var f fatalError; return errors.As(err, &f) }

// wsURL is the stream endpoint for a session.
func wsURL(c *api.Client, id string, since int) string {
	u, _ := url.Parse(c.BaseURL)
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/sessions/" + id + "/stream"
	u.RawPath = ""
	u.RawQuery = url.Values{"since": {strconv.Itoa(since)}, "client": {"cli"}}.Encode()
	return u.String()
}

// runSocket holds one connection. It returns whether any frame arrived, and the reason it ended.
func runSocket(ctx context.Context, c *api.Client, id string, last *int, o Options, deliver func(api.Event) error) (bool, error) {
	hdr := http.Header{}
	hdr.Set("User-Agent", c.UserAgent)
	if c.Workspace != "" {
		hdr.Set(api.HeaderWorkspace, c.Workspace)
	}
	refreshed := false
	var conn *websocket.Conn
	for {
		if c.Tokens != nil {
			tok, err := c.Tokens.Token(ctx)
			if err != nil {
				return false, fatalError{err}
			}
			if tok != "" {
				hdr.Set("Authorization", "Bearer "+tok)
			}
		}
		dctx, cancel := context.WithTimeout(ctx, c.Timeout)
		var resp *http.Response
		var err error
		conn, resp, err = websocket.Dial(dctx, wsURL(c, id, *last), &websocket.DialOptions{HTTPClient: c.HTTP, HTTPHeader: hdr})
		cancel()
		if err == nil {
			break
		}
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				if !refreshed && c.Tokens != nil {
					refreshed = true
					if _, ok := c.Tokens.Refresh(ctx); ok {
						continue
					}
				}
				return false, fatalError{api.MapError(resp.StatusCode, body, resp.Header.Get("x-request-id"))}
			case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
				return false, fatalError{api.MapError(resp.StatusCode, body, resp.Header.Get("x-request-id"))}
			}
		}
		return false, err
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(32 << 20)

	pctx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		t := time.NewTicker(o.PingEvery)
		defer t.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-t.C:
				pc, cancel := context.WithTimeout(pctx, 10*time.Second)
				err := conn.Ping(pc)
				cancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()

	got := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return got, err
		}
		got = true
		ev, ok := decodeFrame(data)
		if !ok {
			continue // presence, metrics and other frames are not events
		}
		if err := deliver(ev); err != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
			if errors.Is(err, ErrStop) {
				return got, err
			}
			return got, fatalError{err}
		}
	}
}

// decodeFrame reads an event frame; other frame kinds (presence, file views, metrics) have no seq.
func decodeFrame(data []byte) (api.Event, bool) {
	var probe struct {
		Seq  *int   `json:"seq"`
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &probe) != nil || probe.Seq == nil || probe.Type == "" {
		return api.Event{}, false
	}
	var ev api.Event
	if json.Unmarshal(data, &ev) != nil {
		return api.Event{}, false
	}
	return ev, true
}

const pollPage = 500

// maxTail is the most events `tail` returns (1 to 1000).
const maxTail = 1000

// poll follows by reading /events?since= every PollInterval.
func poll(ctx context.Context, c *api.Client, id string, last *int, o Options, deliver func(api.Event) error) error {
	for {
		for {
			page, err := c.EventsPage(ctx, id, api.EventsQuery{Since: *last, Limit: pollPage})
			if err != nil {
				return err
			}
			for _, ev := range page.Events {
				if err := deliver(ev); err != nil {
					if errors.Is(err, ErrStop) {
						return nil
					}
					return err
				}
			}
			if !page.HasMore || len(page.Events) == 0 {
				break
			}
		}
		if err := o.Sleep(ctx, o.PollInterval); err != nil {
			return err
		}
	}
}

// History reads every event after since, page by page. A server that ignores `limit` answers the
// whole log in one page.
func History(ctx context.Context, c *api.Client, id string, since int) ([]api.Event, error) {
	var all []api.Event
	for {
		page, err := c.EventsPage(ctx, id, api.EventsQuery{Since: since, Limit: pollPage})
		if err != nil {
			return nil, err
		}
		progressed := false
		for _, ev := range page.Events {
			if ev.Seq > since {
				since, progressed = ev.Seq, true
				all = append(all, ev)
			}
		}
		if !page.HasMore || !progressed {
			return all, nil
		}
	}
}

// Tail reads the last n events. It asks the server for `tail=n`, and keeps the last n client
// side in case the server ignores it and answers the whole log.
func Tail(ctx context.Context, c *api.Client, id string, n int) ([]api.Event, error) {
	if n > maxTail { // the server's cap: read the log and cut it here
		all, err := History(ctx, c, id, 0)
		if err != nil {
			return nil, err
		}
		return all[max(0, len(all)-n):], nil
	}
	page, err := c.EventsPage(ctx, id, api.EventsQuery{Tail: n})
	if err != nil {
		return nil, err
	}
	evs := page.Events
	if len(evs) > n {
		evs = evs[len(evs)-n:]
	}
	return evs, nil
}
