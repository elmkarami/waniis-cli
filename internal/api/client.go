// Package api is the HTTP adapter for Waniis's public /v1 API: base URL, bearer and workspace
// headers, timeouts, retries, idempotency keys and error mapping to exit codes.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

// Header names the API reads and writes.
const (
	HeaderWorkspace      = "x-rafiiq-workspace"
	HeaderIdempotencyKey = "Idempotency-Key"
)

// tokenPattern matches Waniis token values (personal and CLI-grant prefixes) for log redaction.
var tokenPattern = regexp.MustCompile(`waniis_(?:pat|cli|ref)_[A-Za-z0-9_\-]+`)

// Redact removes anything that looks like a token from s.
func Redact(s string) string { return tokenPattern.ReplaceAllString(s, "waniis_***") }

// TokenSource supplies the bearer token for a request. Refresh is called once after a 401 and
// returns a new token when the credential can be renewed.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	Refresh(ctx context.Context) (string, bool)
}

// Client talks to the API. Build it with New; the zero value is not usable.
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	Tokens     TokenSource // nil for unauthenticated calls (device flow)
	Workspace  string
	UserAgent  string
	Timeout    time.Duration
	MaxRetries int
	BaseDelay  time.Duration
	Sleep      func(context.Context, time.Duration) error
	NewKey     func() string
	Log        io.Writer // verbose request log, redacted; nil disables
}

// New builds a Client with the defaults: 30 s timeout, 3 retries, real sleeping, random UUID keys.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTP:       httpClient,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		BaseDelay:  500 * time.Millisecond,
		Sleep:      SleepContext,
		NewKey:     NewUUID,
	}
}

// SleepContext sleeps for d or until ctx ends.
func SleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NewUUID returns a random (version 4) UUID.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system random source failing is a bug, not an expected failure
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Request describes one API call.
type Request struct {
	Method string
	Path   string // e.g. "/v1/sessions"
	Query  url.Values
	Body   any // marshalled as JSON when not nil
	// Idempotent makes the call send an Idempotency-Key (generated unless Key is set) and so
	// retry safely on network errors, 429 and 5xx.
	Idempotent bool
	Key        string
	// Anonymous skips the Authorization header (the device flow).
	Anonymous bool
}

// Response is what a successful call returns besides the decoded body.
type Response struct {
	Status    int
	Header    http.Header
	RequestID string
}

// Do performs the request and decodes a JSON response into out (when out is not nil).
func (c *Client) Do(ctx context.Context, req Request, out any) (*Response, error) {
	var payload []byte
	if req.Body != nil {
		var err error
		if payload, err = json.Marshal(req.Body); err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
	}
	key := ""
	if req.Idempotent || req.Key != "" {
		key = req.Key
		if key == "" {
			key = c.NewKey()
		}
	}
	retryable := key != "" || req.Method == http.MethodGet || req.Method == http.MethodHead ||
		req.Method == http.MethodPut || req.Method == http.MethodDelete

	target := c.BaseURL + req.Path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}

	refreshed := false
	var lastErr error
	for attempt := 0; ; attempt++ {
		resp, wait, err := c.once(ctx, req, target, payload, key, out)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		var ee *exitcode.Error
		if errors.As(err, &ee) && ee.Status == http.StatusUnauthorized && !refreshed && c.Tokens != nil && !req.Anonymous {
			refreshed = true
			if _, ok := c.Tokens.Refresh(ctx); ok {
				attempt--
				continue
			}
		}
		if !retryable || wait < 0 || attempt >= c.MaxRetries || ctx.Err() != nil {
			break
		}
		delay := wait
		if delay == 0 {
			delay = c.BaseDelay << attempt
		}
		c.logf("retrying in %s (attempt %d of %d)", delay, attempt+1, c.MaxRetries)
		if err := c.Sleep(ctx, delay); err != nil {
			break
		}
	}
	return nil, lastErr
}

// once makes one attempt. The returned duration is how long to wait before a retry: zero means
// "use the backoff", negative means "do not retry this failure".
func (c *Client) once(ctx context.Context, req Request, target string, payload []byte, key string, out any) (*Response, time.Duration, error) {
	rctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	hr, err := http.NewRequestWithContext(rctx, req.Method, target, body)
	if err != nil {
		return nil, -1, exitcode.Usagef("bad request: %v", err)
	}
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("User-Agent", c.UserAgent)
	if payload != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	if c.Workspace != "" {
		hr.Header.Set(HeaderWorkspace, c.Workspace)
	}
	if key != "" {
		hr.Header.Set(HeaderIdempotencyKey, key)
	}
	if c.Tokens != nil && !req.Anonymous {
		tok, err := c.Tokens.Token(ctx)
		if err != nil {
			return nil, -1, err
		}
		if tok != "" {
			hr.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	c.logf("> %s %s", req.Method, Redact(target))
	res, err := c.HTTP.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, ctx.Err()
		}
		c.logf("< network error: %s", Redact(err.Error()))
		return nil, 0, networkError(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, 0, networkError(err)
	}
	rid := res.Header.Get("x-request-id")
	c.logf("< %d %s", res.StatusCode, rid)
	info := &Response{Status: res.StatusCode, Header: res.Header, RequestID: rid}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if out != nil && len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return info, -1, &exitcode.Error{Code: exitcode.General, APICode: "bad_response",
					Message: "the API sent a response the CLI couldn't read", Status: res.StatusCode, RequestID: rid, Cause: err}
			}
		}
		return info, 0, nil
	}
	apiErr := MapError(res.StatusCode, data, rid)
	wait := time.Duration(-1)
	if res.StatusCode == 429 || res.StatusCode >= 500 && res.StatusCode != 501 {
		wait = retryAfter(res.Header.Get("Retry-After"))
	}
	return info, wait, apiErr
}

func (c *Client) logf(format string, args ...any) {
	if c.Log != nil {
		fmt.Fprintf(c.Log, "waniis: "+format+"\n", args...)
	}
}

func networkError(err error) *exitcode.Error {
	msg := "couldn't reach Waniis"
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		msg = "the request to Waniis timed out"
	}
	return &exitcode.Error{Code: exitcode.Unavailable, APICode: "network_error", Message: msg,
		Hint: "Check your connection and try again; with --verbose to see the request.", Cause: err}
}

// retryAfter reads a Retry-After header in seconds; zero when absent or unreadable.
func retryAfter(v string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		if n > 60 {
			n = 60
		}
		return time.Duration(n) * time.Second
	}
	return 0
}
