package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// OAuth device-flow seams. They are package vars so the -login flow can be
// tested without driving Google's endpoints.
var (
	oauthRequestDeviceCode = ytm.RequestDeviceCode
	oauthPollToken         = ytm.PollToken

	// oauthConfirmSignIn probes the account behind a freshly minted token using a
	// throwaway Bearer client. A non-nil error means the probe itself failed
	// (network/timeout) — the sign-in state is unknown, not confirmed anonymous.
	oauthConfirmSignIn = func(cfg *config.Config, tok *ytm.OAuthToken) (name string, signedIn bool, err error) {
		c := ytm.NewClient(nil)
		c.UseOAuth(tok, oauthCredsFrom(cfg), "")
		c.SetAuthUser(cfg.AuthUser)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.AccountInfo(ctx)
	}
)

// oauthTokenPath returns the OAuth token storage path, DataDir()/oauth.json.
func oauthTokenPath() string {
	return filepath.Join(config.DataDir(), "oauth.json")
}

// oauthCredsFrom builds the OAuth client credentials from config.
func oauthCredsFrom(cfg *config.Config) ytm.OAuthCreds {
	return ytm.OAuthCreds{ClientID: cfg.OAuthClientID, ClientSecret: cfg.OAuthClientSecret}
}

// missingCredsMessage is the clear error + one-time-setup pointer printed when
// -login is run without OAuth client credentials in config.
func missingCredsMessage() string {
	return "tubeamp: OAuth sign-in needs your own Google OAuth client.\n" +
		"  Set oauth_client_id and oauth_client_secret in " + filepath.Join(config.ConfigDir(), "config.yaml") + ".\n" +
		"  One-time setup: create a Google Cloud project, enable \"YouTube Data API v3\", then create an\n" +
		"  OAuth client of type \"TV and Limited Input devices\" and copy its client ID/secret into config.yaml.\n" +
		"  See the README \"OAuth (durable sign-in)\" section for the full walkthrough."
}

// runLogin implements `tubeamp -login`: the OAuth device flow. It requires OAuth
// client credentials in config, requests a device code, prints the verification
// URL + user code, polls until the user authorizes (or denies / the code
// expires), writes the token to DataDir()/oauth.json (0600), and confirms the
// account with a Bearer AccountInfo probe. Returns a process exit code.
func runLogin(out, errOut io.Writer, cfg *config.Config) int {
	if cfg.OAuthClientID == "" || cfg.OAuthClientSecret == "" {
		fmt.Fprintln(errOut, missingCredsMessage())
		return 1
	}
	creds := oauthCredsFrom(cfg)

	codeCtx, cancelCode := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCode()
	dc, err := oauthRequestDeviceCode(codeCtx, creds)
	if err != nil {
		fmt.Fprintln(errOut, "tubeamp: could not start sign-in:", err)
		return 1
	}

	fmt.Fprintln(out, "To sign in to YouTube Music, open this URL in any browser:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "    "+dc.VerificationURL)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "and enter the code:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "    "+dc.UserCode)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Waiting for authorization… (Ctrl-C to abort)")

	// Bound polling by the code's lifetime so -login never hangs forever.
	life := time.Duration(dc.ExpiresIn) * time.Second
	if life <= 0 {
		life = 15 * time.Minute
	}
	pollCtx, cancelPoll := context.WithTimeout(context.Background(), life)
	defer cancelPoll()

	tok, err := oauthPollToken(pollCtx, creds, dc)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(errOut, "tubeamp: timed out waiting for authorization — run 'tubeamp -login' again")
		} else {
			fmt.Fprintln(errOut, "tubeamp:", err)
		}
		return 1
	}

	path := oauthTokenPath()
	if err := tok.Save(path); err != nil {
		fmt.Fprintln(errOut, "tubeamp: could not save token:", err)
		return 1
	}

	name, signedIn, err := oauthConfirmSignIn(cfg, tok)
	switch {
	case err != nil:
		// The token is saved and valid; only the confirmation probe failed.
		fmt.Fprintf(out, "signed in (token saved), but could not confirm the account (%v) — check later with tubeamp -status\n", err)
	case signedIn:
		fmt.Fprintf(out, "signed in as %s\n", name)
	default:
		fmt.Fprintln(out, "signed in (token saved), but the account did not resolve a name")
	}
	return 0
}

// runLogout implements `tubeamp -logout`: it deletes the OAuth token file. A
// missing file is reported, not an error. Returns a process exit code.
func runLogout(out, errOut io.Writer) int {
	path := oauthTokenPath()
	err := os.Remove(path)
	switch {
	case err == nil:
		fmt.Fprintln(out, "signed out (removed oauth.json)")
		return 0
	case os.IsNotExist(err):
		fmt.Fprintln(out, "not signed in via OAuth (no oauth.json)")
		return 0
	default:
		fmt.Fprintln(errOut, "tubeamp:", err)
		return 1
	}
}
