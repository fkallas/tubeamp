package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/fkallas/tubeamp/internal/auth"
	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// authImport reads the named browser's YouTube cookies and returns the assembled
// Cookie header. It is a package var so tests can stub the import without driving
// a real browser store.
var authImport = auth.ImportFromBrowser

// confirmSignIn builds a throwaway client from the just-written auth file and
// probes the live sign-in state under a short timeout. A non-nil err means the
// probe itself failed (file unreadable, network down, timeout) — the sign-in
// state is UNKNOWN, which the caller must not conflate with a confirmed
// anonymous result. It is a package var so tests can confirm without touching
// the network.
var confirmSignIn = func(cfg *config.Config) (name string, signedIn bool, err error) {
	a, err := ytm.LoadAuth(filepath.Join(config.DataDir(), "auth"))
	if err != nil {
		return "", false, err
	}
	c := ytm.NewClient(a)
	c.SetAuthUser(cfg.AuthUser)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.AccountInfo(ctx)
}

// authBrowserFlag implements the -auth flag. It doubles as a value flag
// (-auth chrome, -auth=chrome) and a bare switch (-auth, taken as "auto").
// IsBoolFlag lets `-auth` stand alone; the space-separated `-auth chrome` form
// leaves "chrome" as a positional that the caller pairs back up via flag.Arg(0).
type authBrowserFlag struct {
	set   bool
	value string
}

func (a *authBrowserFlag) String() string { return a.value }

func (a *authBrowserFlag) Set(v string) error {
	a.set = true
	if v == "true" { // bare -auth via the IsBoolFlag path
		a.value = "auto"
		return nil
	}
	a.value = v
	return nil
}

func (a *authBrowserFlag) IsBoolFlag() bool { return true }

// authBrowser resolves the chosen browser after flag.Parse: the explicit value
// (-auth=chrome), the auto switch (bare -auth), or the space-separated form
// (-auth chrome, where chrome lands as the first positional). Returns "" when
// -auth was not given.
func authBrowser(f *authBrowserFlag) string {
	if !f.set {
		return ""
	}
	if f.value == "auto" && flag.NArg() > 0 {
		return flag.Arg(0)
	}
	if f.value == "" {
		return "auto"
	}
	return f.value
}

// runAuthImport implements `tubeamp -auth <browser>`: it imports the sign-in
// cookies from the named browser, writes them to the auth file via the shared
// atomic 0600 writer, remembers the browser as the import source (config), then
// confirms the session with a live AccountInfo probe. It never prints cookie
// values. Returns a process exit code.
func runAuthImport(out, errOut io.Writer, cfg *config.Config, browser string) int {
	if browser == "" {
		browser = "auto"
	}

	header, err := authImport(browser)
	if err != nil {
		fmt.Fprintln(errOut, "tubeamp:", err)
		return 1
	}

	path := filepath.Join(config.DataDir(), "auth")
	if err := ytm.WriteAuthFile(path, header); err != nil {
		fmt.Fprintln(errOut, "tubeamp:", err)
		return 1
	}

	// Remember the import source so the UI can auto-refresh a stale session.
	cfg.AuthBrowser = browser
	if err := cfg.Save(); err != nil {
		// Non-fatal: the cookie is already written; just warn.
		fmt.Fprintln(errOut, "tubeamp: could not save config:", err)
	}

	name, signedIn, err := confirmSignIn(cfg)
	switch {
	case err != nil:
		// The import itself succeeded and the auth file is written; only the
		// confirmation probe failed (offline, timeout). Don't blame the cookies.
		fmt.Fprintf(out, "imported from %s, but could not confirm the sign-in (%v) — check later with tubeamp -status\n", browser, err)
	case signedIn:
		fmt.Fprintf(out, "signed in as %s\n", name)
	default:
		fmt.Fprintf(out, "imported, but YouTube still resolved anonymous — are you logged into %s?\n", browser)
	}
	return 0
}
