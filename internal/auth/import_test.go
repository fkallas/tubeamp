package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

// cookies builds a []*http.Cookie from name=value pairs (and, where given, a
// domain) without touching a browser store, so the pure assembler can be tested
// against a synthetic set.
func cookie(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value}
}

// TestAssembleCookieHeader_canonical asserts the assembled header emits the known
// sign-in cookies in the fixed canonical order, then any extras alphabetically.
func TestAssembleCookieHeader_canonical(t *testing.T) {
	in := []*http.Cookie{
		cookie("SID", "sid"),
		cookie("FOO", "bar"), // extra (not in cookieOrder) => trails, alphabetical
		cookie("SIDCC", "cc"),
		cookie("LOGIN_INFO", "li"),
		cookie("__Secure-3PAPISID", "3p"),
		cookie("HSID", "hsid"),
		cookie("SAPISID", "sap"),
		cookie("AAA", "z"), // another extra
	}

	got, err := AssembleCookieHeader(in)
	if err != nil {
		t.Fatalf("AssembleCookieHeader: %v", err)
	}
	want := "HSID=hsid; SAPISID=sap; __Secure-3PAPISID=3p; SID=sid; LOGIN_INFO=li; SIDCC=cc; AAA=z; FOO=bar"
	if got != want {
		t.Errorf("header =\n  %q\nwant\n  %q", got, want)
	}
}

// TestAssembleCookieHeader_dedupAndEmpty asserts the first non-empty value wins
// for a duplicated name and that empty-valued cookies are dropped entirely.
func TestAssembleCookieHeader_dedupAndEmpty(t *testing.T) {
	in := []*http.Cookie{
		cookie("SAPISID", "first"),
		cookie("SAPISID", "second"), // duplicate => ignored
		cookie("SID", ""),           // empty => dropped
		cookie("HSID", "h"),
	}

	got, err := AssembleCookieHeader(in)
	if err != nil {
		t.Fatalf("AssembleCookieHeader: %v", err)
	}
	want := "HSID=h; SAPISID=first"
	if got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
}

// TestAssembleCookieHeader_sapisidFallback asserts __Secure-3PAPISID alone (no
// plain SAPISID) is accepted as the required signing cookie.
func TestAssembleCookieHeader_sapisidFallback(t *testing.T) {
	in := []*http.Cookie{
		cookie("SID", "sid"),
		cookie("__Secure-3PAPISID", "3p"),
	}
	if _, err := AssembleCookieHeader(in); err != nil {
		t.Errorf("expected success with __Secure-3PAPISID, got %v", err)
	}
}

// TestAssembleCookieHeader_noSAPISID asserts the typed ErrNoSAPISID is returned
// (matchable with errors.Is) when no signing cookie is present.
func TestAssembleCookieHeader_noSAPISID(t *testing.T) {
	in := []*http.Cookie{
		cookie("SID", "sid"),
		cookie("HSID", "hsid"),
		cookie("YSC", "ysc"),
	}
	_, err := AssembleCookieHeader(in)
	if !errors.Is(err, ErrNoSAPISID) {
		t.Errorf("err = %v, want ErrNoSAPISID", err)
	}
}

// TestRelevantDomain covers the YouTube/Google domain filter.
func TestRelevantDomain(t *testing.T) {
	cases := map[string]bool{
		"music.youtube.com":   true,
		".youtube.com":        true,
		"youtube.com":         true,
		".google.com":         true,
		"accounts.google.com": true,
		"example.com":         false,
		"notyoutube.com":      false,
		"google.com.evil.io":  false,
		"":                    false,
	}
	for domain, want := range cases {
		if got := relevantDomain(domain); got != want {
			t.Errorf("relevantDomain(%q) = %v, want %v", domain, got, want)
		}
	}
}

// TestImportFromBrowser_unsupported asserts a bogus browser name is rejected
// without touching any store.
func TestImportFromBrowser_unsupported(t *testing.T) {
	if _, _, err := ImportFromBrowser("netscape4"); err == nil {
		t.Error("expected error for unsupported browser, got nil")
	}
}

// fakeStore is an injected storeReader: it assembles a header from a fixed cookie
// set (so the selector's profile logic can be tested without a real browser),
// reports whether it is a browser's default profile, and names its browser. A
// non-nil readErr simulates a store-level read failure (e.g. a Keychain denial).
type fakeStore struct {
	cookies   []*http.Cookie
	isDefault bool
	name      string
	readErr   error
}

func (f fakeStore) isDefaultProfile() bool { return f.isDefault }

func (f fakeStore) browserName() string { return f.name }

func (f fakeStore) header(context.Context) (string, error) {
	if f.readErr != nil {
		return "", f.readErr
	}
	return AssembleCookieHeader(f.cookies)
}

func sapisidSet(value string) []*http.Cookie {
	return []*http.Cookie{cookie("SAPISID", value), cookie("SID", "sid")}
}

// TestPickStoreHeader_skipsEmpty: among several candidate profiles, the one whose
// cookie DB actually carries a SAPISID is chosen, not an empty stale profile that
// happens to be iterated first. The winning store's browser is attributed.
func TestPickStoreHeader_skipsEmpty(t *testing.T) {
	stores := []storeReader{
		fakeStore{cookies: []*http.Cookie{cookie("YSC", "y")}, name: "chrome"},  // no SAPISID
		fakeStore{cookies: sapisidSet("real"), name: "firefox"},                 // the real session
		fakeStore{cookies: []*http.Cookie{cookie("PREF", "p")}, name: "safari"}, // another empty one
	}
	got, browser, err := pickStoreHeader(context.Background(), stores)
	if err != nil {
		t.Fatalf("pickStoreHeader: %v", err)
	}
	if !strings.Contains(got, "SAPISID=real") {
		t.Errorf("header = %q, want the SAPISID-bearing store", got)
	}
	if browser != "firefox" {
		t.Errorf("browser = %q, want firefox (the store that supplied the session)", browser)
	}
}

// TestPickStoreHeader_prefersDefaultProfile: when several profiles carry a
// SAPISID, the browser's DEFAULT profile wins regardless of iteration order —
// this is the fix for kooky picking a stale *.default over the active
// *.default-release that profiles.ini marks as default.
func TestPickStoreHeader_prefersDefaultProfile(t *testing.T) {
	stale := fakeStore{cookies: sapisidSet("stale")}                    // secondary profile
	active := fakeStore{cookies: sapisidSet("active"), isDefault: true} // default profile

	for _, tc := range []struct {
		name   string
		stores []storeReader
	}{
		{"stale-first", []storeReader{stale, active}},
		{"default-first", []storeReader{active, stale}},
	} {
		got, _, err := pickStoreHeader(context.Background(), tc.stores)
		if err != nil {
			t.Fatalf("%s: pickStoreHeader: %v", tc.name, err)
		}
		if !strings.Contains(got, "SAPISID=active") {
			t.Errorf("%s: header = %q, want the default profile's session", tc.name, got)
		}
	}
}

// TestPickStoreHeader_nonDefaultFallback: a session that lives only on a
// non-default profile is still found when no default profile carries one.
func TestPickStoreHeader_nonDefaultFallback(t *testing.T) {
	stores := []storeReader{
		fakeStore{cookies: []*http.Cookie{cookie("YSC", "y")}, isDefault: true}, // default, but empty
		fakeStore{cookies: sapisidSet("secondary")},                             // session on a secondary profile
	}
	got, _, err := pickStoreHeader(context.Background(), stores)
	if err != nil {
		t.Fatalf("pickStoreHeader: %v", err)
	}
	if !strings.Contains(got, "SAPISID=secondary") {
		t.Errorf("header = %q, want the secondary profile's session", got)
	}
}

// TestPickStoreHeader_noSession: when no candidate holds a SAPISID, ErrNoSAPISID
// is returned (matchable with errors.Is).
func TestPickStoreHeader_noSession(t *testing.T) {
	stores := []storeReader{
		fakeStore{cookies: []*http.Cookie{cookie("YSC", "y")}},
		fakeStore{cookies: []*http.Cookie{cookie("PREF", "p")}},
	}
	if _, _, err := pickStoreHeader(context.Background(), stores); !errors.Is(err, ErrNoSAPISID) {
		t.Errorf("err = %v, want ErrNoSAPISID", err)
	}
}

// TestPickStoreHeader_readErrorSurfaced: a genuine read failure (not just "no
// session") is surfaced over the bare ErrNoSAPISID when nothing else yields a
// session, so callers see the informative error (e.g. a Keychain denial).
func TestPickStoreHeader_readErrorSurfaced(t *testing.T) {
	wantErr := errors.New("keychain denied")
	stores := []storeReader{
		fakeStore{readErr: wantErr},
		fakeStore{cookies: []*http.Cookie{cookie("YSC", "y")}}, // no session
	}
	_, _, err := pickStoreHeader(context.Background(), stores)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want the read error", err)
	}
}

// TestPickStoreHeader_readErrorButSessionWins: a read failure on one profile does
// not stop a sibling profile's session from being used.
func TestPickStoreHeader_readErrorButSessionWins(t *testing.T) {
	stores := []storeReader{
		fakeStore{readErr: errors.New("locked")},
		fakeStore{cookies: sapisidSet("ok")},
	}
	got, _, err := pickStoreHeader(context.Background(), stores)
	if err != nil {
		t.Fatalf("pickStoreHeader: %v", err)
	}
	if !strings.Contains(got, "SAPISID=ok") {
		t.Errorf("header = %q, want the readable store's session", got)
	}
}

// TestIsBrowserRunning_unsupported asserts the running-browser detection reports
// false (never errors) for an unsupported browser id, without touching the
// process table.
func TestIsBrowserRunning_unsupported(t *testing.T) {
	if IsBrowserRunning("netscape4") {
		t.Error("IsBrowserRunning(unsupported) = true, want false")
	}
	if IsBrowserRunning("auto") {
		t.Error("IsBrowserRunning(auto) = true, want false (no single process to attribute)")
	}
}

// TestIsBrowserRunning_live exercises the real process-table check. It is skipped
// by default; set TUBEAMP_LIVE_PROC=1 (and optionally TUBEAMP_LIVE_PROC_BROWSER)
// to run it against the machine's actual processes.
func TestIsBrowserRunning_live(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE_PROC") != "1" {
		t.Skip("set TUBEAMP_LIVE_PROC=1 (and optionally TUBEAMP_LIVE_PROC_BROWSER) to run the real process check")
	}
	browser := os.Getenv("TUBEAMP_LIVE_PROC_BROWSER")
	if browser == "" {
		browser = "firefox"
	}
	t.Logf("IsBrowserRunning(%q) = %v", browser, IsBrowserRunning(browser))
}

// TestImportFromBrowser_live exercises the real kooky-backed import against the
// machine's actual browser. It is skipped by default (it needs a signed-in
// browser and, on macOS, Keychain consent) and never logs the cookie value.
func TestImportFromBrowser_live(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE_IMPORT") != "1" {
		t.Skip("set TUBEAMP_LIVE_IMPORT=1 (and optionally TUBEAMP_LIVE_IMPORT_BROWSER) to run the real browser import")
	}
	browser := os.Getenv("TUBEAMP_LIVE_IMPORT_BROWSER") // "" => auto
	header, source, err := ImportFromBrowser(browser)
	if err != nil {
		t.Fatalf("ImportFromBrowser(%q): %v", browser, err)
	}
	// Never print the header — it is a live credential. Only assert structure.
	if !strings.Contains(header, "SAPISID") {
		t.Error("imported header is missing a SAPISID cookie")
	}
	if source == "" || source == "auto" {
		t.Errorf("sourceBrowser = %q, want a concrete browser id", source)
	}
}
