package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Repo is a repository a session works on.
type Repo struct {
	FullName string `json:"fullName"`
	CloneURL string `json:"cloneUrl,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// PullRequest is a session's pull request link.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Person is someone on a session.
type Person struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Login string `json:"login"`
	Role  string `json:"role,omitempty"`
}

// Session is the list/summary shape (CORE-06 session index). Raw keeps the server's own JSON so
// --json passes every field through, including ones this CLI doesn't know yet.
type Session struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Repos        []Repo          `json:"repos"`
	Status       string          `json:"status"`
	Stage        string          `json:"stage"`
	PullRequest  *PullRequest    `json:"pullRequest"`
	CreatedAt    time.Time       `json:"createdAt"`
	LastActiveAt time.Time       `json:"lastActiveAt"`
	Visibility   string          `json:"visibility"`
	Owner        *Person         `json:"owner"`
	Raw          json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes a session and keeps the raw bytes.
func (s *Session) UnmarshalJSON(b []byte) error {
	type plain Session
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = Session(p)
	s.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON writes the server's own JSON when known, so output keeps every field.
func (s Session) MarshalJSON() ([]byte, error) {
	if len(s.Raw) > 0 {
		return s.Raw, nil
	}
	type plain Session
	return json.Marshal(plain(s))
}

// Interpretation says how a natural-language search was understood (FR-9). The fields are the
// spec's `interpreted: { repos, since, prState, text }`; the rest are kept in Raw.
type Interpretation struct {
	Repos   []string        `json:"repos,omitempty"`
	People  []string        `json:"people,omitempty"`
	Status  []string        `json:"status,omitempty"`
	Stage   string          `json:"stage,omitempty"`
	Since   string          `json:"since,omitempty"`
	Until   string          `json:"until,omitempty"`
	PRState string          `json:"prState,omitempty"`
	Text    string          `json:"text,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the interpretation and keeps the raw bytes.
func (i *Interpretation) UnmarshalJSON(b []byte) error {
	type plain Interpretation
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*i = Interpretation(p)
	i.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON writes the server's own JSON when known.
func (i Interpretation) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	type plain Interpretation
	return json.Marshal(plain(i))
}

// SessionPage is one page of sessions. The API's list answers `{sessions, next}` today and
// `{items, next}` per the CLI spec; both are read.
type SessionPage struct {
	Items       []Session       `json:"items"`
	Sessions    []Session       `json:"sessions"`
	Next        *string         `json:"next"`
	NextCursor  *string         `json:"nextCursor"`
	Interpreted *Interpretation `json:"interpreted"`
}

// Cursor is the cursor for the next page, empty when this was the last.
func (p *SessionPage) Cursor() string {
	switch {
	case p.Next != nil:
		return *p.Next
	case p.NextCursor != nil:
		return *p.NextCursor
	}
	return ""
}

// All returns the page's sessions whichever key the server used.
func (p *SessionPage) All() []Session {
	if p.Items != nil {
		return p.Items
	}
	return p.Sessions
}

// ListFilter is every filter `sessions list` and `sessions search` can send.
type ListFilter struct {
	Filter      string // mine|shared|archived|all
	Status      []string
	Repo        []string
	Author      string
	Stage       string
	Since       string
	Until       string
	HasPR       bool
	Limit       int
	Cursor      string
	Query       string // search only: the natural-language text
	NoInterpret bool   // search only
}

func (f ListFilter) values() url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("filter", f.Filter)
	for _, s := range f.Status {
		q.Add("status", s)
	}
	for _, r := range f.Repo {
		q.Add("repo", r)
	}
	set("author", f.Author)
	set("stage", f.Stage)
	set("since", f.Since)
	set("until", f.Until)
	if f.HasPR {
		q.Set("hasPr", "true")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	set("cursor", f.Cursor)
	set("q", f.Query)
	if f.NoInterpret {
		q.Set("interpret", "false")
	}
	return q
}

// ListSessions is GET /v1/sessions.
func (c *Client) ListSessions(ctx context.Context, f ListFilter) (*SessionPage, error) {
	var page SessionPage
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions", Query: f.values()}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// SearchSessions is GET /v1/sessions/search.
func (c *Client) SearchSessions(ctx context.Context, f ListFilter) (*SessionPage, error) {
	var page SessionPage
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/search", Query: f.values()}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetSession is GET /v1/sessions/:id.
func (c *Client) GetSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/" + url.PathEscape(id)}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// RepoInput is a repository to start a session on.
type RepoInput struct {
	CloneURL string `json:"cloneUrl"`
	Branch   string `json:"branch,omitempty"`
}

// PromptInput is the first prompt of a new session: a protocol `prompt.submit` command.
type PromptInput struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Mode   string `json:"mode,omitempty"`
	Model  string `json:"model,omitempty"`
	Source string `json:"source"`
}

// CreateSessionInput is the body of POST /v1/sessions (CORE-06 plus G6's first prompt).
type CreateSessionInput struct {
	Title      string       `json:"title,omitempty"`
	Visibility string       `json:"visibility,omitempty"`
	TeamID     string       `json:"teamId,omitempty"`
	Repos      []RepoInput  `json:"repos,omitempty"`
	Stage      string       `json:"stage,omitempty"`
	Prompt     *PromptInput `json:"prompt,omitempty"`
}

// CreatedSession is the 201 answer to a create.
type CreatedSession struct {
	ID     string          `json:"id"`
	Events json.RawMessage `json:"events,omitempty"`
}

// CreateSession is POST /v1/sessions with an Idempotency-Key.
func (c *Client) CreateSession(ctx context.Context, in CreateSessionInput, key string) (*CreatedSession, error) {
	var out CreatedSession
	_, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/sessions", Body: in, Idempotent: true, Key: key}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// StopRun is POST /v1/sessions/:id/commands with a run.stop command.
func (c *Client) StopRun(ctx context.Context, id string) error {
	body := map[string]any{"type": "run.stop"}
	_, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/sessions/" + url.PathEscape(id) + "/commands", Body: body, Idempotent: true}, nil)
	return err
}

// Event is one session event (FND-05): its sequence number, type and payload.
type Event struct {
	Seq     int             `json:"seq"`
	Type    string          `json:"type"`
	At      string          `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// RunCompleted is the payload of a run.completed event.
type RunCompleted struct {
	RunID   string  `json:"runId"`
	Outcome string  `json:"outcome"`
	Summary string  `json:"summary"`
	CostUSD float64 `json:"costUsd"`
}

// Events is GET /v1/sessions/:id/events?since=.
func (c *Client) Events(ctx context.Context, id string, since int) ([]Event, error) {
	var out struct {
		Events []Event `json:"events"`
	}
	q := url.Values{"since": {strconv.Itoa(since)}}
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/" + url.PathEscape(id) + "/events", Query: q}, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

// ShareLink is the session's share link as its owner sees it.
type ShareLink struct {
	Role      string `json:"role"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
}

// SetShareLink is PUT /v1/sessions/:id/share-link.
func (c *Client) SetShareLink(ctx context.Context, id, role string) (*ShareLink, error) {
	var out struct {
		Link *ShareLink `json:"link"`
	}
	_, err := c.Do(ctx, Request{Method: http.MethodPut, Path: "/v1/sessions/" + url.PathEscape(id) + "/share-link", Body: map[string]string{"role": role}}, &out)
	if err != nil {
		return nil, err
	}
	return out.Link, nil
}

// RevokeShareLink is DELETE /v1/sessions/:id/share-link.
func (c *Client) RevokeShareLink(ctx context.Context, id string) error {
	_, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: "/v1/sessions/" + url.PathEscape(id) + "/share-link"}, nil)
	return err
}

// People is GET /v1/sessions/:id/people.
func (c *Client) People(ctx context.Context, id string) ([]Person, error) {
	var out struct {
		People []Person `json:"people"`
	}
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/sessions/" + url.PathEscape(id) + "/people"}, &out); err != nil {
		return nil, err
	}
	return out.People, nil
}

// Invite is POST /v1/sessions/:id/people.
func (c *Client) Invite(ctx context.Context, id, login, role string) (*Person, error) {
	var out struct {
		Person *Person `json:"person"`
	}
	_, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/sessions/" + url.PathEscape(id) + "/people",
		Body: map[string]string{"login": login, "role": role}, Idempotent: true}, &out)
	if err != nil {
		return nil, err
	}
	return out.Person, nil
}

// RemovePerson is DELETE /v1/sessions/:id/people/:userId.
func (c *Client) RemovePerson(ctx context.Context, id, userID string) error {
	_, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: "/v1/sessions/" + url.PathEscape(id) + "/people/" + url.PathEscape(userID)}, nil)
	return err
}

// SetVisibility is PUT /v1/sessions/:id/visibility.
func (c *Client) SetVisibility(ctx context.Context, id, visibility, teamID string) error {
	body := map[string]string{"visibility": visibility}
	if teamID != "" {
		body["teamId"] = teamID
	}
	_, err := c.Do(ctx, Request{Method: http.MethodPut, Path: "/v1/sessions/" + url.PathEscape(id) + "/visibility", Body: body}, nil)
	return err
}

// Me is the signed-in person with their workspaces (GET /v1/me).
type Me struct {
	ID         string `json:"id"`
	Login      string `json:"login"`
	Name       string `json:"name"`
	Workspaces []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"workspaces"`
}

// Me is GET /v1/me.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/me"}, &me); err != nil {
		return nil, err
	}
	return &me, nil
}
