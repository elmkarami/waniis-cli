package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

type staticTokens struct{ tok string }

func (s staticTokens) Token(context.Context) (string, error)  { return s.tok, nil }
func (s staticTokens) Refresh(context.Context) (string, bool) { return "", false }

func testClient(srv *httptest.Server, sleeps *[]time.Duration) *Client {
	c := New(srv.URL, srv.Client())
	c.Sleep = func(_ context.Context, d time.Duration) error { *sleeps = append(*sleeps, d); return nil }
	c.UserAgent = "test"
	return c
}

func TestGETRetriesOn503HonoringRetryAfter(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := testClient(srv, &sleeps)
	var out struct{ OK bool }
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x"}, &out); err != nil || !out.OK {
		t.Fatalf("err=%v out=%+v", err, out)
	}
	if len(sleeps) != 2 || sleeps[0] != 2*time.Second || sleeps[1] != 2*time.Second {
		t.Fatalf("Retry-After not honored: %v", sleeps)
	}
}

func TestBackoffGrowsExponentially(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer srv.Close()
	var sleeps []time.Duration
	c := testClient(srv, &sleeps)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x"}, nil)
	if exitcode.Of(err) != exitcode.Unavailable {
		t.Fatalf("exit = %d, err = %v", exitcode.Of(err), err)
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	if len(sleeps) != 3 {
		t.Fatalf("sleeps = %v", sleeps)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", sleeps, want)
		}
	}
}

func TestPOSTWithoutKeyIsNeverRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(503) }))
	defer srv.Close()
	var sleeps []time.Duration
	c := testClient(srv, &sleeps)
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "/x", Body: map[string]string{"a": "b"}}, nil)
	if err == nil || n.Load() != 1 || len(sleeps) != 0 {
		t.Fatalf("a keyless POST must not retry: calls=%d sleeps=%v err=%v", n.Load(), sleeps, err)
	}
}

func TestPOSTWithKeyRetriesSendingTheSameKeyAndBody(t *testing.T) {
	var keys []string
	var bodies []string
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		var b bytes.Buffer
		b.ReadFrom(r.Body)
		bodies = append(bodies, b.String())
		if n.Add(1) < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"s"}`))
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := testClient(srv, &sleeps)
	var out struct{ ID string }
	if _, err := c.Do(context.Background(), Request{Method: "POST", Path: "/x", Body: map[string]string{"a": "b"}, Idempotent: true}, &out); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("keys = %v", keys)
	}
	for _, b := range bodies {
		if b != bodies[0] || !strings.Contains(b, `"a":"b"`) {
			t.Fatalf("body changed between attempts: %v", bodies)
		}
	}
	if len(keys[0]) != 36 || keys[0][14] != '4' {
		t.Fatalf("key should be a UUID v4: %q", keys[0])
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"not_found"}`))
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := testClient(srv, &sleeps)
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x"}, nil)
	if exitcode.Of(err) != exitcode.NotFound || n.Load() != 1 {
		t.Fatalf("calls=%d err=%v", n.Load(), err)
	}
}

func TestHeadersAndRedaction(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone(); w.Write([]byte(`{}`)) }))
	defer srv.Close()
	var sleeps []time.Duration
	c := testClient(srv, &sleeps)
	c.Tokens = staticTokens{"waniis_pat_secret123"}
	c.Workspace = "ws_9"
	var log bytes.Buffer
	c.Log = &log
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/v1/me"}, nil); err != nil {
		t.Fatal(err)
	}
	if got.Get("Authorization") != "Bearer waniis_pat_secret123" || got.Get("x-rafiiq-workspace") != "ws_9" || got.Get("User-Agent") != "test" {
		t.Fatalf("headers: %v", got)
	}
	if strings.Contains(log.String(), "secret123") {
		t.Fatalf("log leaks the token: %s", log.String())
	}
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/v1/me", Anonymous: true}, nil); err != nil || got.Get("Authorization") != "" {
		t.Fatalf("anonymous requests carry no token: %v %v", err, got)
	}
}

func TestRedact(t *testing.T) {
	in := "GET /x?token=waniis_cli_AbC-123_x and waniis_pat_zzz"
	out := Redact(in)
	if strings.Contains(out, "AbC") || strings.Contains(out, "zzz") {
		t.Fatalf("not redacted: %s", out)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := New(srv.URL, srv.Client())
	c.Sleep = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	_, err := c.Do(ctx, Request{Method: "GET", Path: "/x"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestSessionRoundTripKeepsRawFields(t *testing.T) {
	var s Session
	if err := s.UnmarshalJSON([]byte(`{"id":"a","title":"t","new":"field"}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := s.MarshalJSON()
	if !strings.Contains(string(b), `"new":"field"`) {
		t.Fatalf("unknown fields lost: %s", b)
	}
}
