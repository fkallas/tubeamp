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
// prints the are-you-logged-in hint.
func TestRunAuthImport_anonymous(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	stubAuthImport(t, "SAPISID=x", nil, "", false)

	var out, errOut bytes.Buffer
	if code := runAuthImport(&out, &errOut, config.Default(), "firefox"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); !strings.Contains(got, "still resolved anonymous") || !strings.Contains(got, "firefox") {
		t.Errorf("stdout = %q, want anonymous hint mentioning firefox", got)
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
