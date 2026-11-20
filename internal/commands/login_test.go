package commands

import (
	"testing"
	"time"

	"github.com/elmkarami/waniis-cli/internal/auth"
)

func TestLoginDeviceFlow(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	h.f.devicePolls = []string{"pending", "slow_down", "pending", "ok"}
	code, out, errOut := h.run("login")
	eq(t, code, 0, "exit code")
	contains(t, errOut, "WDJB-MJHT")
	contains(t, errOut, "https://app.waniis.com/device")
	contains(t, out, "Signed in to "+h.f.srv.URL+" as maya.")
	eq(t, len(h.opened), 1, "browser opened once")
	eq(t, h.opened[0], "https://app.waniis.com/device?code=WDJB-MJHT", "browser URL carries the code")
	// poll interval 5s, +5s after slow_down
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	eq(t, len(h.sleeps), len(want), "polls")
	for i, d := range want {
		eq(t, h.sleeps[i], d, "poll delay")
	}
	cred, _ := h.store.Load(h.f.srv.URL)
	if cred == nil || cred.Token != testAccess || cred.RefreshToken != testRef || cred.Kind != auth.KindGrant {
		t.Fatalf("credentials not stored correctly: %+v", cred)
	}
	eq(t, cred.ExpiresAt, h.now.Add(time.Hour), "expiry from expires_in")
	notContains(t, out+errOut, testAccess)
	notContains(t, out+errOut, testRef)
	body := h.f.last("POST", "/auth/device/code").Body
	eq(t, body["client_id"], any("waniis-cli"), "client id")
	eq(t, body["device_name"], any("CLI on maya-mbp"), "device name")
	eq(t, h.f.last("POST", "/auth/device/token").Body["grant_type"], any("urn:ietf:params:oauth:grant-type:device_code"), "grant type")
}

func TestLoginDeviceFlowNoBrowserWhenNotInteractive(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("login")
	eq(t, code, 0, "exit code")
	eq(t, len(h.opened), 0, "no browser without a terminal")
	contains(t, errOut, "WDJB-MJHT")
}

func TestLoginDeviceFlowDeniedAndExpired(t *testing.T) {
	for step, wantMsg := range map[string]string{"denied": "denied", "expired": "expired"} {
		h := newHarness(t)
		h.f.devicePolls = []string{"pending", step}
		code, _, errOut := h.run("login")
		eq(t, code, 3, step+" exit code")
		contains(t, errOut, wantMsg)
		if c, _ := h.store.Load(h.f.srv.URL); c != nil {
			t.Fatalf("%s: nothing should be stored", step)
		}
	}
}

func TestLoginDeviceFlowExpiresByClock(t *testing.T) {
	h := newHarness(t)
	// the server never approves; the 600 s the code lives run out by our own clock
	for i := 0; i < 200; i++ {
		h.f.devicePolls = append(h.f.devicePolls, "pending")
	}
	code, _, errOut := h.run("login")
	eq(t, code, 3, "exit code")
	contains(t, errOut, "expired")
}

func TestLoginWithTokenFlagAndStdinAndEnv(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("login", "--token", testPAT)
	eq(t, code, 0, "exit code")
	contains(t, out, "as maya")
	cred, _ := h.store.Load(h.f.srv.URL)
	if cred == nil || cred.Token != testPAT || cred.Kind != auth.KindPersonal || cred.Login != "maya" {
		t.Fatalf("stored: %+v", cred)
	}
	notContains(t, out, testPAT)

	h2 := newHarness(t)
	h2.in = testPAT + "\n"
	code, _, _ = h2.run("login", "--with-token")
	eq(t, code, 0, "stdin exit code")

	h3 := newHarness(t)
	h3.env["WANIIS_TOKEN"] = testPAT
	code, _, _ = h3.run("login")
	eq(t, code, 0, "env exit code")
	if c, _ := h3.store.Load(h3.f.srv.URL); c == nil {
		t.Fatal("env token should be stored by login")
	}
}

func TestLoginWithBadTokenIsNotStored(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("login", "--token", "rafiiq_pat_wrong")
	eq(t, code, 3, "exit code")
	contains(t, errOut, "waniis login")
	if c, _ := h.store.Load(h.f.srv.URL); c != nil {
		t.Fatal("a rejected token must not be stored")
	}
}

func TestLoginJSONHasNoToken(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("--json", "login", "--token", testPAT)
	eq(t, code, 0, "exit code")
	m := mustJSON(t, out)
	eq(t, m["signedIn"], any(true), "signedIn")
	notContains(t, out, testPAT)
}

func TestLogoutRevokesGrantAndForgets(t *testing.T) {
	h := newHarness(t)
	_ = h.store.Save(&auth.Credentials{Host: h.f.srv.URL, Token: testAccess, RefreshToken: testRef, Kind: auth.KindGrant})
	code, out, _ := h.run("logout")
	eq(t, code, 0, "exit code")
	contains(t, out, "Signed out")
	if c, _ := h.store.Load(h.f.srv.URL); c != nil {
		t.Fatal("credentials must be deleted")
	}
	eq(t, len(h.f.revoked), 1, "revoke calls")
	eq(t, h.f.revoked[0], testRef, "the refresh token is what gets revoked")
	// signing out twice is fine
	code, out, _ = h.run("logout")
	eq(t, code, 0, "second logout")
	contains(t, out, "not signed in")
}

func TestLogoutPersonalTokenIsLocalOnly(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, _, _ := h.run("logout")
	eq(t, code, 0, "exit code")
	eq(t, len(h.f.revoked), 0, "a personal token is not revoked by logout")
}

func TestWhoami(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, out, _ := h.run("whoami")
	eq(t, code, 0, "exit code")
	contains(t, out, "maya")
	contains(t, out, "personal API token")
	notContains(t, out, testPAT)

	code, out, _ = h.run("--json", "--workspace", "ws_1", "whoami")
	eq(t, code, 0, "json exit code")
	m := mustJSON(t, out)
	eq(t, m["login"], any("maya"), "login")
	eq(t, m["workspace"], any("ws_1"), "workspace")
	eq(t, m["tokenKind"], any("pat"), "token kind")
	notContains(t, out, testPAT)
	eq(t, h.f.last("GET", "/v1/me").Header.Get("x-rafiiq-workspace"), "ws_1", "workspace header")
}

func TestWhoamiNotSignedIn(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("whoami")
	eq(t, code, 3, "exit code")
	contains(t, errOut, "waniis login")
	eq(t, len(h.f.reqs), 0, "no request without a token")
}

func TestEnvTokenOverridesStoredCredential(t *testing.T) {
	h := newHarness(t)
	_ = h.store.Save(&auth.Credentials{Host: h.f.srv.URL, Token: "rafiiq_pat_stale", Kind: auth.KindPersonal})
	h.env["WANIIS_TOKEN"] = testPAT
	code, out, _ := h.run("whoami")
	eq(t, code, 0, "exit code")
	contains(t, out, "WANIIS_TOKEN")
}

func TestExpiredGrantIsRefreshedAndRotated(t *testing.T) {
	h := newHarness(t)
	h.f.validToken = "rafiiq_cli_old" // the API will accept the rotated token only after refresh
	_ = h.store.Save(&auth.Credentials{Host: h.f.srv.URL, Token: "rafiiq_cli_old", RefreshToken: testRef, Kind: auth.KindGrant,
		ExpiresAt: h.now.Add(30 * time.Second), Login: "maya"})
	code, _, _ := h.run("whoami")
	eq(t, code, 0, "exit code")
	c, _ := h.store.Load(h.f.srv.URL)
	eq(t, c.Token, "rafiiq_cli_rotated", "token rotated")
	eq(t, c.RefreshToken, "rafiiq_cli_refresh_rotated", "refresh token rotated")
	eq(t, c.Login, "maya", "identity kept")
}

func TestUnauthorizedTriggersOneRefresh(t *testing.T) {
	h := newHarness(t)
	h.f.validToken = "rafiiq_cli_something_else"
	// A reused or revoked refresh token: the server refuses it, the CLI doesn't loop.
	_ = h.store.Save(&auth.Credentials{Host: h.f.srv.URL, Token: "rafiiq_cli_revoked", RefreshToken: "rafiiq_cli_bad", Kind: auth.KindGrant,
		ExpiresAt: h.now.Add(time.Hour)})
	code, _, errOut := h.run("whoami")
	eq(t, code, 3, "exit code")
	contains(t, errOut, "waniis login")
	eq(t, h.f.count("POST", "/auth/device/token"), 1, "exactly one refresh attempt")
	eq(t, h.f.count("GET", "/v1/me"), 1, "the request isn't repeated when the refresh fails")
}

func TestUnauthorizedRefreshesAndRetriesOnce(t *testing.T) {
	h := newHarness(t)
	h.f.validToken = "rafiiq_cli_rotated" // what the server accepts after the refresh
	_ = h.store.Save(&auth.Credentials{Host: h.f.srv.URL, Token: "rafiiq_cli_revoked", RefreshToken: testRef, Kind: auth.KindGrant,
		ExpiresAt: h.now.Add(time.Hour)})
	code, _, _ := h.run("whoami")
	eq(t, code, 0, "exit code")
	eq(t, h.f.count("GET", "/v1/me"), 2, "retried once with the new token")
}
