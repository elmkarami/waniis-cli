package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/exitcode"
)

// ClientID identifies the CLI to the device flow.
const ClientID = "waniis-cli"

// Grant types (RFC 8628 and RFC 6749).
const (
	GrantDeviceCode   = "urn:ietf:params:oauth:grant-type:device_code"
	GrantRefreshToken = "refresh_token"
)

// DeviceCode is the answer to POST /auth/device/code (RFC 8628 section 3.2).
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// TokenResponse is the answer to POST /auth/device/token.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// DeviceFlow runs the OAuth 2.0 device authorization grant against the API.
type DeviceFlow struct {
	Client *api.Client // used anonymously
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
	// Show is called once with the code to present to the person (URL and user code).
	Show func(DeviceCode)
}

// Start asks for a device code.
func (d *DeviceFlow) Start(ctx context.Context, deviceName, scope string) (*DeviceCode, error) {
	body := map[string]string{"client_id": ClientID, "device_name": deviceName}
	if scope != "" {
		body["scope"] = scope
	}
	var dc DeviceCode
	if _, err := d.Client.Do(ctx, api.Request{Method: http.MethodPost, Path: "/auth/device/code", Body: body, Anonymous: true}, &dc); err != nil {
		return nil, err
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, exitcode.New(exitcode.General, "bad_response", "the sign-in service sent an incomplete device code")
	}
	return &dc, nil
}

// Poll waits for the person to approve, at the server's interval, backing off on slow_down.
func (d *DeviceFlow) Poll(ctx context.Context, dc *DeviceCode) (*Credentials, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := d.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	if dc.ExpiresIn <= 0 {
		deadline = d.Now().Add(10 * time.Minute)
	}
	for {
		if err := d.Sleep(ctx, interval); err != nil {
			return nil, err
		}
		if d.Now().After(deadline) {
			return nil, expired()
		}
		var tr TokenResponse
		_, err := d.Client.Do(ctx, api.Request{Method: http.MethodPost, Path: "/auth/device/token", Anonymous: true,
			Body: map[string]string{"grant_type": GrantDeviceCode, "device_code": dc.DeviceCode, "client_id": ClientID}}, &tr)
		if err == nil {
			return credentialsFrom(tr, d.Now()), nil
		}
		var ee *exitcode.Error
		if !errors.As(err, &ee) {
			return nil, err
		}
		switch ee.APICode {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			return nil, &exitcode.Error{Code: exitcode.Auth, APICode: "access_denied", Message: "The sign-in request was denied.",
				Hint: "Run `waniis login` again and approve it in the browser."}
		case "expired_token":
			return nil, expired()
		}
		return nil, err
	}
}

func expired() *exitcode.Error {
	return &exitcode.Error{Code: exitcode.Auth, APICode: "expired_token", Message: "The sign-in code expired before it was approved.",
		Hint: "Run `waniis login` again."}
}

func credentialsFrom(tr TokenResponse, now time.Time) *Credentials {
	c := &Credentials{Token: tr.AccessToken, RefreshToken: tr.RefreshToken, Kind: KindOf(tr.AccessToken)}
	if tr.ExpiresIn > 0 {
		c.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return c
}

// NewRefresher builds a Refresher that calls POST /auth/device/token with the refresh grant.
func NewRefresher(client *api.Client, now func() time.Time) Refresher {
	return func(ctx context.Context, refreshToken string) (*Credentials, error) {
		var tr TokenResponse
		_, err := client.Do(ctx, api.Request{Method: http.MethodPost, Path: "/auth/device/token", Anonymous: true,
			Body: map[string]string{"grant_type": GrantRefreshToken, "refresh_token": refreshToken, "client_id": ClientID}}, &tr)
		if err != nil {
			return nil, err
		}
		return credentialsFrom(tr, now()), nil
	}
}
