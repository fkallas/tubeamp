package ytm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OAuth device-flow constants, mirrored from ytmusicapi
// (ytmusicapi/auth/oauth/credentials.py + constants.py). The OAuth client type is
// "TV and Limited Input devices"; because Google revoked the shared TV
// credentials ytmusicapi once bundled, the client_id/client_secret are supplied
// by the user (see config.OAuthClientID/OAuthClientSecret).
const (
	oauthScope = "https://www.googleapis.com/auth/youtube"

	// oauthGrantDevice is the device-code token-exchange grant ytmusicapi uses
	// ("http://oauth.net/grant_type/device/1.0"); Google still accepts this older
	// form for the TV/limited-input client alongside the RFC 8628 urn.
	oauthGrantDevice  = "http://oauth.net/grant_type/device/1.0"
	oauthGrantRefresh = "refresh_token"

	// oauthCredUA is the User-Agent ytmusicapi sends with the device-code and
	// token requests (OAUTH_USER_AGENT = USER_AGENT + " Cobalt/Version").
	oauthCredUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:88.0) Gecko/20100101 Firefox/88.0 Cobalt/Version"
)

// Device-flow endpoints and the HTTP client used for the OAuth credential
// requests. They are package vars (not consts) so tests can point them at an
// httptest server without touching Google. Override under a single-threaded test
// and restore on cleanup.
var (
	oauthCodeURL  = "https://www.youtube.com/o/oauth2/device/code"
	oauthTokenURL = "https://oauth2.googleapis.com/token"
	oauthHTTP     = &http.Client{Timeout: 30 * time.Second}

	// oauthSleep waits d or until ctx is cancelled. It is a package var so the
	// device-flow polling test can drive the loop with a no-op sleep + short
	// interval and run fast.
	oauthSleep = func(ctx context.Context, d time.Duration) {
		if d <= 0 {
			return
		}
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
)

// OAuthCreds are the user-supplied Google OAuth client credentials for the
// "TV and Limited Input devices" client type.
type OAuthCreds struct {
	ClientID     string
	ClientSecret string
}

// OAuthToken is a stored OAuth bearer token, persisted as DataDir()/oauth.json.
// Field names match ytmusicapi's oauth.json so a token created by either tool
// loads in the other.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiresAt    int64  `json:"expires_at"` // unix epoch seconds the access token expires
	ExpiresIn    int64  `json:"expires_in"` // seconds-to-expiry from the last grant (informational)
}

// DeviceCode is the parsed response of the device-code request, the first step
// of the device flow.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	VerificationURL string `json:"verification_url"`
}

// OAuthError is a Google OAuth endpoint error carrying the JSON "error" code
// (e.g. "authorization_pending", "slow_down", "access_denied", "invalid_grant").
type OAuthError struct {
	Code        string
	Description string
	Status      int
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("ytm: oauth error %q: %s", e.Code, e.Description)
	}
	return fmt.Sprintf("ytm: oauth error %q", e.Code)
}

// tokenResponse is the JSON returned by the token endpoint for both the success
// and error shapes (the error fields are empty on success).
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	Scope            string `json:"scope"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// LoadOAuthToken reads and parses the OAuth token JSON at path. It errors if the
// file is missing/unreadable, the JSON is malformed, or it carries no tokens.
func LoadOAuthToken(path string) (*OAuthToken, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ytm.LoadOAuthToken: %w", err)
	}
	var t OAuthToken
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("ytm.LoadOAuthToken: parse %q: %w", path, err)
	}
	if t.AccessToken == "" && t.RefreshToken == "" {
		return nil, fmt.Errorf("ytm.LoadOAuthToken: %q has no tokens", path)
	}
	return &t, nil
}

// Save writes the token to path as indented JSON via the shared atomic 0600
// writer (the same one the cookie auth file uses), creating the parent dir 0700.
func (t *OAuthToken) Save(path string) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("ytm: marshal oauth token: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("ytm: create oauth dir: %w", err)
	}
	return atomicWriteFile(path, data)
}

// IsExpired reports whether the access token has expired or is within 60s of
// expiring (matching ytmusicapi's is_expiring window). A zero ExpiresAt — e.g. a
// token loaded from an older file — counts as expired so a refresh is forced.
func (t *OAuthToken) IsExpired(now time.Time) bool {
	return t.ExpiresAt-now.Unix() < 60
}

// Authorization returns the value for the Authorization header,
// "<token_type> <access_token>" (defaulting the type to "Bearer").
func (t *OAuthToken) Authorization() string {
	tt := t.TokenType
	if tt == "" {
		tt = "Bearer"
	}
	return tt + " " + t.AccessToken
}

// Refresh requests a new access token from the token endpoint using the refresh
// token and the client credentials, updating the token in place. The refresh
// token itself is unchanged. A revoked/mismatched refresh token surfaces as an
// *OAuthError (e.g. code "invalid_grant").
func (t *OAuthToken) Refresh(ctx context.Context, creds OAuthCreds) error {
	form := url.Values{
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"grant_type":    {oauthGrantRefresh},
		"refresh_token": {t.RefreshToken},
	}
	tr, err := oauthTokenRequest(ctx, form)
	if err != nil {
		return err
	}
	t.AccessToken = tr.AccessToken
	t.ExpiresIn = int64(tr.ExpiresIn)
	t.ExpiresAt = time.Now().Unix() + int64(tr.ExpiresIn)
	if tr.Scope != "" {
		t.Scope = tr.Scope
	}
	if tr.TokenType != "" {
		t.TokenType = tr.TokenType
	}
	return nil
}

// RequestDeviceCode performs the first device-flow step: POST the OAuth scope +
// client_id to the device-code endpoint and return the user code, verification
// URL, and polling interval.
func RequestDeviceCode(ctx context.Context, creds OAuthCreds) (DeviceCode, error) {
	form := url.Values{
		"client_id": {creds.ClientID},
		"scope":     {oauthScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCode{}, fmt.Errorf("ytm: build device-code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", oauthCredUA)

	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("ytm: device-code request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DeviceCode{}, fmt.Errorf("ytm: read device-code response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DeviceCode{}, oauthErrorFromBody(body, resp.StatusCode)
	}
	var dc DeviceCode
	if err := json.Unmarshal(body, &dc); err != nil {
		return DeviceCode{}, fmt.Errorf("ytm: parse device-code response: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return DeviceCode{}, fmt.Errorf("ytm: device-code response missing codes")
	}
	return dc, nil
}

// PollToken polls the token endpoint at the device code's interval until the
// user authorizes (returns the token), denies, or the code expires. It respects
// the device-flow back-off signals: "authorization_pending" keeps waiting and
// "slow_down" widens the interval by 5s (per RFC 8628). A cancelled ctx aborts.
func PollToken(ctx context.Context, creds OAuthCreds, dc DeviceCode) (*OAuthToken, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	form := url.Values{
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"grant_type":    {oauthGrantDevice},
		"code":          {dc.DeviceCode},
	}
	for {
		oauthSleep(ctx, interval)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tr, err := oauthTokenRequest(ctx, form)
		if err != nil {
			var oerr *OAuthError
			if errors.As(err, &oerr) {
				switch oerr.Code {
				case "authorization_pending":
					continue
				case "slow_down":
					interval += 5 * time.Second
					continue
				case "access_denied":
					return nil, fmt.Errorf("ytm: sign-in denied")
				case "expired_token":
					return nil, fmt.Errorf("ytm: device code expired before authorization")
				}
			}
			return nil, err
		}
		tok := &OAuthToken{
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			Scope:        tr.Scope,
			TokenType:    tr.TokenType,
			ExpiresIn:    int64(tr.ExpiresIn),
			ExpiresAt:    time.Now().Unix() + int64(tr.ExpiresIn),
		}
		return tok, nil
	}
}

// oauthTokenRequest POSTs a form to the token endpoint and parses the response.
// A JSON "error" field (on any status) yields an *OAuthError; a non-2xx response
// without one yields a plain status error.
func oauthTokenRequest(ctx context.Context, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("ytm: build oauth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", oauthCredUA)

	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("ytm: oauth token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("ytm: read oauth token response: %w", err)
	}
	var tr tokenResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &tr); err != nil {
			return tokenResponse{}, fmt.Errorf("ytm: parse oauth token response: %w", err)
		}
	}
	if tr.Error != "" {
		return tr, &OAuthError{Code: tr.Error, Description: tr.ErrorDescription, Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tr, fmt.Errorf("ytm: oauth token endpoint HTTP %d", resp.StatusCode)
	}
	return tr, nil
}

// oauthErrorFromBody builds an error from a non-2xx OAuth body, preferring the
// JSON "error" field when present.
func oauthErrorFromBody(body []byte, status int) error {
	var tr tokenResponse
	if len(body) > 0 && json.Unmarshal(body, &tr) == nil && tr.Error != "" {
		return &OAuthError{Code: tr.Error, Description: tr.ErrorDescription, Status: status}
	}
	return fmt.Errorf("ytm: oauth HTTP %d", status)
}
