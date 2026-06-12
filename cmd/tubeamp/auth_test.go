package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fkallas/tubeamp/internal/config"
)

// stubAuthImport replaces the browser import and sign-in confirmation with canned
// results so the -auth flow can be tested without a browser or the network. The
// originals are restored on cleanup.
func stubAuthImport(t *testing.T, header string, importErr error, name string, signedIn bool) {
	t.Helper()
	stubAuthImportConfirmErr(t, header, importErr, name, signedIn, nil)
}

// stubAuthImportConfirmErr is stubAuthImport with a confirmation-probe error.
func stubAuthImportConfirmErr(t *testing.T, header string, importErr error, name string, signedIn bool, confirmErr error) {
	t.Helper()
	origImport, origConfirm := authImport, confirmSignIn
	authImport = func(string) (string, error) { return header, importErr }
	confirmSignIn = func(*config.Config) (string, bool, error) { return name, signedIn, confirmErr }
	t.Cleanup(func() { authImport, confirmSignIn = origImport, origConfirm })
}

// stubBrowserRunning pins the running-browser detection so the -auth message is
// deterministic in tests (the real check shells out to pgrep).
func stubBrowserRunning(t *testing.T, running bool) {
	t.Helper()
	orig := browserRunning
	browserRunning = func(string) bool { return running }
	t.Cleanup(func() { browserRunning = orig })
}

// TestRunAuthImport_signedIn: a successful import writes the auth file (0600),
// persists the browser to config, prints "signed in as <name>", and never leaks
// the cookie value to stdout/stderr.
func TestRunAuthImport_signedIn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	const secret = "SAPISID=topsecret; SID=alsosecret"
	stubAuthImport(t, secret, nil, "Felipe Kallas", true)

	cfg := config.Default()
	var out, errOut bytes.Buffer
	if code := runAuthImport(&out, &errOut, cfg, "chrome"); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, errOut.String())
	}

	if got := out.String(); !strings.Contains(got, "signed in as Felipe Kallas") {
		t.Errorf("stdout = %q, want signed-in message", got)
	}
	// The cookie value must never appear in any output.
	if strings.Contains(out.String(), "topsecret") || strings.Contains(errOut.String(), "topsecret") {
		t.Fatal("cookie value leaked to output")
	}

	// Auth file written with the shared atomic 0600 writer.
	authPath := filepath.Join(config.DataDir(), "auth")
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	if strings.TrimSpace(string(data)) != secret {
		t.Errorf("auth file = %q, want %q", strings.TrimSpace(string(data)), secret)
	}
	fi, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("auth file perms = %o, want 600", fi.Mode().Perm())
	}

	// Import source persisted to config.
	if cfg.AuthBrowser != "chrome" {
		t.Errorf("cfg.AuthBrowser = %q, want chrome", cfg.AuthBrowser)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if loaded.AuthBrowser != "chrome" {
		t.Errorf("persisted AuthBrowser = %q, want chrome", loaded.AuthBrowser)
	}
}

// TestRunAuthImport_anonymous: a successful import that still resolves anonymous
// while the browser is NOT running prints the are-you-logged-in hint.
func TestRunAuthImport_anonymous(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	stubAuthImport(t, "SAPISID=x", nil, "", false)
	stubBrowserRunning(t, false)

	var out, errOut bytes.Buffer
	if code := runAuthImport(&out, &errOut, config.Default(), "firefox"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "still resolved anonymous") || !strings.Contains(got, "firefox") {
		t.Errorf("stdout = %q, want anonymous hint mentioning firefox", got)
	}
	if strings.Contains(got, "is running") {
		t.Errorf("stdout = %q: must not give the quit-and-retry advice when the browser is not running", got)
	}
}

// TestRunAuthImport_anonymousRunning: a successful import that resolves anonymous
// while the browser IS running replaces the "are you logged in?" line with the
// quit-the-browser-and-retry advice (a running browser holds fresh cookies in
// memory/WAL, so the on-disk snapshot is stale).
func TestRunAuthImport_anonymousRunning(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	stubAuthImport(t, "SAPISID=x", nil, "", false)
	stubBrowserRunning(t, true)

	var out, errOut bytes.Buffer
	if code := runAuthImport(&out, &errOut, config.Default(), "firefox"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "firefox is running") || !strings.Contains(got, "Quit firefox") {
		t.Errorf("stdout = %q, want the quit-and-retry advice for a running browser", got)
	}
	if strings.Contains(got, "are you logged into") {
		t.Errorf("stdout = %q: the running-browser case must replace the are-you-logged-in line", got)
	}
}

// TestAnonymousAdvice covers the pure message selector: signed-in => no advice;
// anonymous + running => quit-and-retry; anonymous + not running => sign-in hint.
func TestAnonymousAdvice(t *testing.T) {
	if got := anonymousAdvice("chrome", true, true); got != "" {
		t.Errorf("signed-in advice = %q, want empty", got)
	}
	if got := anonymousAdvice("chrome", false, true); got != "" {
		t.Errorf("signed-in advice (not running) = %q, want empty", got)
	}
	running := anonymousAdvice("chrome", true, false)
	if !strings.Contains(running, "chrome is running") || !strings.Contains(running, "Quit chrome") || !strings.Contains(running, "tubeamp -auth chrome") {
		t.Errorf("running advice = %q, want quit-and-retry guidance", running)
	}
	if strings.Contains(running, "are you logged into") {
		t.Errorf("running advice = %q: must not include the are-you-logged-in line", running)
	}
	notRunning := anonymousAdvice("chrome", false, false)
	if !strings.Contains(notRunning, "are you logged into chrome") {
		t.Errorf("not-running advice = %q, want the are-you-logged-in hint", notRunning)
	}
	if strings.Contains(notRunning, "is running") {
		t.Errorf("not-running advice = %q: must not claim the browser is running", notRunning)
	}
}

// TestRunAuthImport_probeError: a successful import whose confirmation probe
// fails (offline, timeout) must NOT print the misleading still-resolved-anonymous
// hint — the sign-in state is unknown, not confirmed anonymous.
func TestRunAuthImport_probeError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	stubAuthImportConfirmErr(t, "SAPISID=x", nil, "", false, context.DeadlineExceeded)

	var out, errOut bytes.Buffer
	if code := runAuthImport(&out, &errOut, config.Default(), "safari"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	if strings.Contains(got, "still resolved anonymous") {
		t.Errorf("stdout = %q: a failed probe must not claim the session resolved anonymous", got)
	}
	if !strings.Contains(got, "could not confirm") {
		t.Errorf("stdout = %q, want a could-not-confirm notice", got)
	}
	// The import did succeed: the auth file must exist.
	if _, err := os.Stat(filepath.Join(config.DataDir(), "auth")); err != nil {
		t.Errorf("auth file missing after successful import: %v", err)
	}
}

// TestRunAuthImport_importError: a failed import reports the error and exits 1
// without writing an auth file.
func TestRunAuthImport_importError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	stubAuthImport(t, "", os.ErrNotExist, "", false)

	var out, errOut bytes.Buffer
	if code := runAuthImport(&out, &errOut, config.Default(), "brave"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "auth")); !os.IsNotExist(err) {
		t.Errorf("auth file should not exist after a failed import (err=%v)", err)
	}
}

// TestAuthBrowser_resolution covers the -auth flag value resolution: explicit
// value, bare switch (auto), and the space-separated positional form.
func TestAuthBrowser_resolution(t *testing.T) {
	if got := authBrowser(&authBrowserFlag{set: false}); got != "" {
		t.Errorf("unset -auth = %q, want empty", got)
	}
	if got := authBrowser(&authBrowserFlag{set: true, value: "chrome"}); got != "chrome" {
		t.Errorf("-auth=chrome = %q, want chrome", got)
	}
	if got := authBrowser(&authBrowserFlag{set: true, value: "auto"}); got != "auto" {
		t.Errorf("bare -auth = %q, want auto", got)
	}
}
