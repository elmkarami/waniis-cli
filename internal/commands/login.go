package commands

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/auth"
	"github.com/elmkarami/waniis-cli/internal/config"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
	"github.com/elmkarami/waniis-cli/internal/output"
)

// authCmd groups login, logout and status under `waniis auth …` as the spec names them.
func (a *app) authCmd() *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Sign in, sign out and see who you are"}
	status := a.whoamiCmd()
	status.Use = "status"
	status.Aliases = nil
	c.AddCommand(a.loginCmd(), a.logoutCmd(), status)
	return c
}

func (a *app) loginCmd() *cobra.Command {
	var token string
	var withToken, noBrowser bool
	var scopes string
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Waniis in your browser, or with a personal token",
		Long: `Sign in with the device flow: the CLI prints a short code and a URL, opens the browser
when it can, and waits until you approve the sign-in there.

For CI and headless machines, use a personal token instead: --token, --with-token (reads the
token from stdin) or the WANIIS_TOKEN environment variable. The token is checked with the API and
kept in your OS keychain (or a file only you can read where there is no keychain).`,
		Example: `  waniis login
  waniis login --no-browser
  echo "$WANIIS_PAT" | waniis login --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if token != "" && withToken {
				return exitcode.Usagef("use either --token or --with-token, not both")
			}
			if withToken {
				b, err := io.ReadAll(io.LimitReader(a.In, 4096))
				if err != nil {
					return err
				}
				token = strings.TrimSpace(string(b))
				if token == "" {
					return exitcode.Usagef("no token on stdin")
				}
			}
			if token == "" {
				token = strings.TrimSpace(a.Getenv(config.EnvToken))
			}
			if token != "" {
				return a.loginWithToken(cmd, token)
			}
			return a.loginDevice(cmd, scopes, noBrowser)
		},
	}
	c.Flags().StringVar(&token, "token", "", "sign in with a personal API token (visible in shell history; prefer --with-token)")
	c.Flags().BoolVar(&withToken, "with-token", false, "read a personal API token from stdin")
	c.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening the browser")
	c.Flags().StringVar(&scopes, "scopes", "read sessions", "scopes to ask for in the browser sign-in (read, sessions, admin)")
	return c
}

// loginWithToken checks a token with GET /v1/me and keeps it.
func (a *app) loginWithToken(cmd *cobra.Command, token string) error {
	rt, err := a.newClient(true)
	if err != nil {
		return err
	}
	rt.Source.EnvTok = token // used for this check only; Source never stores it
	me, err := rt.Client.Me(cmd.Context())
	if err != nil {
		return err
	}
	cred := &auth.Credentials{Host: rt.Base, Token: token, Kind: auth.KindOf(token), UserID: me.ID, Login: me.Login}
	return a.finishLogin(rt.Base, cred, me)
}

func (a *app) loginDevice(cmd *cobra.Command, scopes string, noBrowser bool) error {
	rt, err := a.newClient(false)
	if err != nil {
		return err
	}
	flow := &auth.DeviceFlow{Client: rt.Client, Now: a.Now, Sleep: a.Sleep}
	name := "CLI"
	if h := a.Hostname(); h != "" {
		name = "CLI on " + h
	}
	dc, err := flow.Start(cmd.Context(), name, scopes)
	if err != nil {
		return err
	}
	link := dc.VerificationURIComplete
	if link == "" {
		link = dc.VerificationURI
	}
	fmt.Fprintf(a.Err, "To sign in, open this page and confirm the code:\n\n  %s\n\n  Code: %s\n\n", dc.VerificationURI, dc.UserCode)
	if !noBrowser && a.interactive() && a.OpenBrowser != nil {
		if err := a.OpenBrowser(link); err == nil {
			fmt.Fprintln(a.Err, "Opened your browser. Waiting for approval…")
		} else {
			fmt.Fprintln(a.Err, "Couldn't open a browser; open the page above yourself. Waiting for approval…")
		}
	} else {
		fmt.Fprintln(a.Err, "Waiting for approval…")
	}
	cred, err := flow.Poll(cmd.Context(), dc)
	if err != nil {
		return err
	}
	// Check the new token and learn who it is, without storing anything yet.
	rt2, err := a.newClient(true)
	if err != nil {
		return err
	}
	rt2.Source.EnvTok = cred.Token
	me, err := rt2.Client.Me(cmd.Context())
	if err != nil {
		return err
	}
	cred.Host, cred.UserID, cred.Login = rt.Base, me.ID, me.Login
	return a.finishLogin(rt.Base, cred, me)
}

func (a *app) finishLogin(base string, cred *auth.Credentials, me *api.Me) error {
	if err := a.saveCredentials(cred); err != nil {
		return err
	}
	who := me.Login
	if who == "" {
		who = me.Name
	}
	if who == "" {
		who = me.ID
	}
	if a.useJSON() {
		return output.JSON(a.Out, map[string]any{"host": base, "user": me, "tokenKind": cred.Kind, "signedIn": true})
	}
	fmt.Fprintf(a.Out, "Signed in to %s as %s.\n", base, who)
	return nil
}

// saveCredentials stores in the keychain, falling back to the 0600 file, and says so on stderr.
func (a *app) saveCredentials(cred *auth.Credentials) error {
	if err := a.Store.Save(cred); err != nil {
		return exitcode.New(exitcode.General, "credential_store", "couldn't save your credentials: "+err.Error()).
			WithHint("Set WANIIS_TOKEN instead, or fix the credential store.")
	}
	return nil
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out: revoke the sign-in and forget the stored credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.newClient(false)
			if err != nil {
				return err
			}
			cred, err := a.Store.Load(rt.Base)
			if err != nil {
				return err
			}
			revoked := false
			if cred != nil && cred.Kind == auth.KindGrant {
				tok := cred.RefreshToken
				if tok == "" {
					tok = cred.Token
				}
				// Best effort: if the server can't be reached the local credentials still go.
				_, rerr := rt.Client.Do(cmd.Context(), api.Request{Method: http.MethodPost, Path: "/auth/device/revoke", Anonymous: true,
					Body: map[string]string{"token": tok, "client_id": auth.ClientID}}, nil)
				revoked = rerr == nil
			}
			if err := a.Store.Delete(rt.Base); err != nil {
				return err
			}
			envSet := strings.TrimSpace(a.Getenv(config.EnvToken)) != ""
			if a.useJSON() {
				return output.JSON(a.Out, map[string]any{"host": rt.Base, "signedOut": true, "hadCredentials": cred != nil, "revoked": revoked, "envTokenStillSet": envSet})
			}
			if cred == nil {
				fmt.Fprintf(a.Out, "You were not signed in to %s.\n", rt.Base)
			} else {
				fmt.Fprintf(a.Out, "Signed out of %s.\n", rt.Base)
			}
			if envSet {
				fmt.Fprintln(a.Err, "WANIIS_TOKEN is still set in your environment and will keep being used.")
			}
			return nil
		},
	}
}

func (a *app) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "whoami",
		Short:   "Show who you're signed in as",
		Example: "  waniis whoami\n  waniis whoami --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.newClient(true)
			if err != nil {
				return err
			}
			me, err := rt.Client.Me(cmd.Context())
			if err != nil {
				return err
			}
			kind, expires := a.credentialInfo(rt)
			workspace := rt.Client.Workspace
			if a.useJSON() {
				out := map[string]any{"id": me.ID, "login": me.Login, "name": me.Name, "host": rt.Base,
					"workspace": nullable(workspace), "tokenKind": kind, "workspaces": me.Workspaces}
				if !expires.IsZero() {
					out["tokenExpiresAt"] = expires.UTC().Format(time.RFC3339)
				}
				return output.JSON(a.Out, out)
			}
			if workspace == "" {
				workspace = "(your default)"
			}
			pairs := [][2]string{{"User", firstNonEmpty(me.Login, me.Name, me.ID)}}
			if me.Name != "" && me.Name != me.Login {
				pairs = append(pairs, [2]string{"Name", me.Name})
			}
			pairs = append(pairs, [2]string{"Host", rt.Base}, [2]string{"Workspace", workspace}, [2]string{"Token", kindLabel(kind)})
			if !expires.IsZero() {
				pairs = append(pairs, [2]string{"Expires", expires.UTC().Format("2006-01-02 15:04 UTC")})
			}
			return output.KeyValues(a.Out, pairs)
		},
	}
}

// credentialInfo says what kind of token is in use and when it expires, never the token itself.
func (a *app) credentialInfo(rt *conn) (string, time.Time) {
	if strings.TrimSpace(a.Getenv(config.EnvToken)) != "" {
		return auth.KindEnv, time.Time{}
	}
	if c, err := a.Store.Load(rt.Base); err == nil && c != nil {
		return c.Kind, c.ExpiresAt
	}
	return "", time.Time{}
}

func kindLabel(kind string) string {
	switch kind {
	case auth.KindEnv:
		return "from WANIIS_TOKEN"
	case auth.KindPersonal:
		return "personal API token"
	case auth.KindGrant:
		return "browser sign-in"
	}
	return "unknown"
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
