package auth

import (
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
	if _, err := ImportFromBrowser("netscape4"); err == nil {
		t.Error("expected error for unsupported browser, got nil")
	}
}

// TestImportFromBrowser_live exercises the real kooky-backed import against the
// machine's actual browser. It is skipped by default (it needs a signed-in
// browser and, on macOS, Keychain consent) and never logs the cookie value.
func TestImportFromBrowser_live(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE_IMPORT") != "1" {
		t.Skip("set TUBEAMP_LIVE_IMPORT=1 (and optionally TUBEAMP_LIVE_IMPORT_BROWSER) to run the real browser import")
	}
	browser := os.Getenv("TUBEAMP_LIVE_IMPORT_BROWSER") // "" => auto
	header, err := ImportFromBrowser(browser)
	if err != nil {
		t.Fatalf("ImportFromBrowser(%q): %v", browser, err)
	}
	// Never print the header — it is a live credential. Only assert structure.
	if !strings.Contains(header, "SAPISID") {
		t.Error("imported header is missing a SAPISID cookie")
	}
}
