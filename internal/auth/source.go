package auth

import (
	"context"
	"sync"
	"time"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

// Refresher exchanges a refresh token for a new credential (the CLI grant's rotating token).
type Refresher func(ctx context.Context, refreshToken string) (*Credentials, error)

// Source picks the bearer token for a host: WANIIS_TOKEN wins over anything stored, then the
// stored credential, refreshed shortly before it expires. It implements api.TokenSource.
type Source struct {
	Host   string
	EnvTok string
	Store  Store
	Renew  Refresher
	Now    func() time.Time

	mu   sync.Mutex
	cred *Credentials
}

// notSignedIn is the error for a missing credential.
func notSignedIn() *exitcode.Error {
	return &exitcode.Error{Code: exitcode.Auth, APICode: "not_signed_in",
		Message: "You're not signed in.", Hint: "Run `waniis login`, or set WANIIS_TOKEN."}
}

func (s *Source) load() (*Credentials, error) {
	if s.cred != nil {
		return s.cred, nil
	}
	c, err := s.Store.Load(s.Host)
	if err != nil {
		return nil, err
	}
	s.cred = c
	return c, nil
}

// Token returns the token to send.
func (s *Source) Token(ctx context.Context) (string, error) {
	if s.EnvTok != "" {
		return s.EnvTok, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load()
	if err != nil {
		return "", err
	}
	if c == nil || c.Token == "" {
		return "", notSignedIn()
	}
	if c.RefreshToken != "" && !c.ExpiresAt.IsZero() && s.Now().Add(60*time.Second).After(c.ExpiresAt) {
		if nc, ok := s.refreshLocked(ctx, c); ok {
			return nc.Token, nil
		}
	}
	return c.Token, nil
}

// Refresh renews the stored grant after a 401. It reports whether a new token is in place.
func (s *Source) Refresh(ctx context.Context) (string, bool) {
	if s.EnvTok != "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load()
	if err != nil || c == nil || c.RefreshToken == "" {
		return "", false
	}
	if nc, ok := s.refreshLocked(ctx, c); ok {
		return nc.Token, true
	}
	return "", false
}

func (s *Source) refreshLocked(ctx context.Context, c *Credentials) (*Credentials, bool) {
	if s.Renew == nil {
		return nil, false
	}
	nc, err := s.Renew(ctx, c.RefreshToken)
	if err != nil || nc == nil {
		return nil, false
	}
	nc.Host, nc.UserID, nc.Login = c.Host, c.UserID, c.Login
	if nc.Kind == "" {
		nc.Kind = c.Kind
	}
	if err := s.Store.Save(nc); err != nil {
		// Keep working with the new token for this process even if it couldn't be saved.
		s.cred = nc
		return nc, true
	}
	s.cred = nc
	return nc, true
}
