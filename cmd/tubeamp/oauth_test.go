package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// stubLogin replaces the device-flow + confirm seams so runLogin can be tested
// without driving Google. The originals are restored on cleanup.
func stubLogin(t *testing.T, dc ytm.DeviceCode, dcErr error, tok *ytm.OAuthToken, pollErr error, name string, signedIn bool, confirmErr error) {
	t.Helper()
	origCode, origPoll, origConfirm := oauthRequestDeviceCode, oauthPollToken, oauthConfirmSignIn
	oauthRequestDeviceCode = func(context.Context, ytm.OAuthCreds) (ytm.DeviceCode, error) { return dc, dcErr }
	oauthPollToken = func(context.Context, ytm.OAuthCreds, ytm.DeviceCode) (*ytm.OAuthToken, error) {
		return tok, pollErr
	}
	oauthConfirmSignIn = func(*config.Config, *ytm.OAuthToken) (string, bool, error) {
		return name, signedIn, confirmErr
	}
	t.Cleanup(func() {
		oauthRequestDeviceCode, oauthPollToken, oauthConfirmSignIn = origCode, origPoll, origConfirm
	})
}

func cfgWithCreds() *config.Config {
	c := config.Default()
	c.OAuthClientID = "cid.apps.googleusercontent.com"
	c.OAuthClientSecret = "secret"
	return c
}

// TestRunLogin_success: a full device flow writes oauth.json (0600), prints the
// verification URL + user code and "signed in as <name>".
func TestRunLogin_success(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dc := ytm.DeviceCode{
		DeviceCode:      "dev-code",
		UserCode:        "ABCD-EFGH",
		VerificationURL: "https://www.google.com/device",
		ExpiresIn:       1800,
		Interval:        5,
	}
	tok := &ytm.OAuthToken{AccessToken: "acc", RefreshToken: "ref", TokenType: "Bearer", ExpiresAt: 1 << 40}
	stubLogin(t, dc, nil, tok, nil, "Felipe Kallas", true, nil)

	var out, errOut bytes.Buffer
	if code := runLogin(&out, &errOut, cfgWithCreds()); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%q)", code, errOut.String())
	}
	got := out.String()
	for _, want := range []string{"https://www.google.com/device", "ABCD-EFGH", "Waiting", "signed in as Felipe Kallas"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout %q missing %q", got, want)
		}
	}

	path := filepath.Join(config.DataDir(), "oauth.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat oauth.json: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("oauth.json perms = %o, want 600", fi.Mode().Perm())
	}
	loaded, err := ytm.LoadOAuthToken(path)
	if err != nil {
		t.Fatalf("LoadOAuthToken: %v", err)
	}
	if loaded.AccessToken != "acc" || loaded.RefreshToken != "ref" {
		t.Errorf("saved token = %+v, want acc/ref", loaded)
	}
}

// TestRunLogin_missingCreds: without client credentials, -login prints the setup
// pointer to stderr and exits 1 without writing a token.
func TestRunLogin_missingCreds(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var out, errOut bytes.Buffer
	if code := runLogin(&out, &errOut, config.Default()); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	msg := errOut.String()
	if !strings.Contains(msg, "oauth_client_id") || !strings.Contains(msg, "TV and Limited Input devices") {
		t.Errorf("stderr = %q, want the setup pointer", msg)
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "oauth.json")); !os.IsNotExist(err) {
		t.Errorf("oauth.json should not exist (err=%v)", err)
	}
}

// TestRunLogin_denied: a denied/expired poll reports the error and exits 1
// without writing a token.
func TestRunLogin_denied(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dc := ytm.DeviceCode{DeviceCode: "dc", UserCode: "X", VerificationURL: "u", ExpiresIn: 60, Interval: 1}
	stubLogin(t, dc, nil, nil, context.DeadlineExceeded, "", false, nil)

	var out, errOut bytes.Buffer
	if code := runLogin(&out, &errOut, cfgWithCreds()); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "timed out") {
		t.Errorf("stderr = %q, want a timeout notice", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "oauth.json")); !os.IsNotExist(err) {
		t.Errorf("oauth.json should not exist after a failed poll (err=%v)", err)
	}
}

// TestRunLogin_confirmError: a saved token whose confirmation probe fails must
// not claim a confirmed account; the token is still written.
func TestRunLogin_confirmError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dc := ytm.DeviceCode{DeviceCode: "dc", UserCode: "X", VerificationURL: "u", ExpiresIn: 60, Interval: 1}
	tok := &ytm.OAuthToken{AccessToken: "acc", RefreshToken: "ref", TokenType: "Bearer", ExpiresAt: 1 << 40}
	stubLogin(t, dc, nil, tok, nil, "", false, context.DeadlineExceeded)

	var out, errOut bytes.Buffer
	if code := runLogin(&out, &errOut, cfgWithCreds()); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "could not confirm") {
		t.Errorf("stdout = %q, want a could-not-confirm notice", out.String())
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "oauth.json")); err != nil {
		t.Errorf("oauth.json should exist after a successful poll: %v", err)
	}
}

// TestRunLogout removes an existing token and reports a missing one cleanly.
func TestRunLogout(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	path := filepath.Join(config.DataDir(), "oauth.json")

	// No token yet: reports cleanly, exit 0.
	var out, errOut bytes.Buffer
	if code := runLogout(&out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "not signed in") {
		t.Errorf("stdout = %q, want not-signed-in notice", out.String())
	}

	// Write a token, then logout removes it.
	tok := &ytm.OAuthToken{AccessToken: "acc", RefreshToken: "ref", TokenType: "Bearer"}
	if err := tok.Save(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runLogout(&out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "signed out") {
		t.Errorf("stdout = %q, want signed-out notice", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("oauth.json should be removed (err=%v)", err)
	}
}
