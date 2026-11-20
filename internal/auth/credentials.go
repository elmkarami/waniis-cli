// Package auth holds the CLI's credentials: where they are kept (OS keychain, with a 0600 file
// as the fallback), how the bearer token is chosen for a request, and the device login flow.
package auth

import (
	"strings"
	"time"
)

// Token kinds, from the token prefixes (DEC-152).
const (
	KindPersonal = "pat" // waniis_pat_…: a personal API token
	KindGrant    = "cli" // waniis_cli_…: a device-flow access token
	KindEnv      = "env" // WANIIS_TOKEN, never stored
)

// Prefixes of the two token kinds.
const (
	PrefixPersonal = "waniis_pat_"
	PrefixGrant    = "waniis_cli_"
)

// Credentials is what is stored for one host. It never leaves the credential store except as an
// Authorization header, and no command prints it.
type Credentials struct {
	Host         string    `json:"host"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	Kind         string    `json:"kind"`
	UserID       string    `json:"userId,omitempty"`
	Login        string    `json:"login,omitempty"`
}

// KindOf guesses the kind of a token from its prefix; unknown prefixes count as personal tokens.
func KindOf(token string) string {
	if strings.HasPrefix(token, PrefixGrant) {
		return KindGrant
	}
	return KindPersonal
}

// Store keeps credentials by host.
type Store interface {
	Load(host string) (*Credentials, error) // nil, nil when there is none
	Save(c *Credentials) error
	Delete(host string) error
}
