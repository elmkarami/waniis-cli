// Package config resolves where the CLI talks to and which workspace it acts in. Precedence is
// explicit: flag, then environment, then default (CLT-15/FR-25).
package config

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

// DefaultAPIURL is the product's single origin for the web app and the API.
const DefaultAPIURL = "https://app.waniis.com"

// Environment variable names read by the CLI. They read "Waniis" because people type them.
const (
	EnvToken     = "WANIIS_TOKEN"
	EnvAPIURL    = "WANIIS_API_URL"
	EnvWorkspace = "WANIIS_WORKSPACE"
	EnvConfigDir = "WANIIS_CONFIG_DIR"
	EnvNoColor   = "NO_COLOR"
)

// Pick returns the first non-empty value: the flag, then the environment, then the fallback.
func Pick(flag string, env string, fallback string) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	return fallback
}

// NormalizeBaseURL validates an API base URL. It must be https, except for localhost, so a token
// is never sent in clear text to another host (CLT-15, Security). Trailing slashes are dropped.
func NormalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", exitcode.Usagef("%q is not a valid API URL", raw)
	}
	host := u.Hostname()
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return "", exitcode.Usagef("the API URL must use https (http is allowed only for localhost): %s", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// AppURL is the web app's origin for a given API base: the API alias maps to the app host so
// that links people open in a browser always land on the web app.
func AppURL(apiBase string) string {
	u, err := url.Parse(apiBase)
	if err != nil {
		return apiBase
	}
	if u.Hostname() == "api.waniis.com" {
		u.Host = "app.waniis.com"
	}
	return strings.TrimRight(u.String(), "/")
}

// Dir is the directory for the CLI's own files: $WANIIS_CONFIG_DIR, else $XDG_CONFIG_HOME/waniis,
// else ~/.config/waniis (%AppData%\waniis on Windows).
func Dir(getenv func(string) string) (string, error) {
	if d := getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := getenv("AppData"); d != "" {
			return filepath.Join(d, "waniis"), nil
		}
	}
	if d := getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "waniis"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "waniis"), nil
}
