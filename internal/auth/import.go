// Package auth imports a YouTube Music sign-in from a local browser profile.
//
// Instead of hand-copying the Cookie header out of devtools (see the manual
// method in the README), tubeamp can read the YouTube/Google sign-in cookies
// straight out of a browser's cookie store via kooky and assemble the canonical
// Cookie header tubeamp's InnerTube client sends. This backs both
// `tubeamp -auth <browser>` and the UI's one-shot stale-session auto-refresh.
//
// AssembleCookieHeader (the pure, fixture-tested core) is split from
// ImportFromBrowser (the kooky-backed reader) so the header assembly can be
// unit-tested without a real browser.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/browserutils/kooky"

	// Register the cookie-store finders for exactly the browsers we support.
	// Blank imports keep the kooky dependency surface scoped to these six
	// stores rather than pulling in every browser via .../browser/all.
	_ "github.com/browserutils/kooky/browser/brave"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/chromium"
	_ "github.com/browserutils/kooky/browser/edge"
	_ "github.com/browserutils/kooky/browser/firefox"
	_ "github.com/browserutils/kooky/browser/safari"
)

// ErrNoSAPISID is returned when an assembled cookie set lacks the SAPISID (or
// __Secure-3PAPISID) cookie the SAPISIDHASH authorization depends on — i.e. the
// browser is not signed into a Google account, or the relevant cookies could not
// be decrypted. It is the typed "no usable cookie set" error callers match with
// errors.Is.
var ErrNoSAPISID = errors.New("auth: no SAPISID cookie found (is the browser signed into YouTube?)")

// ErrNoStore is returned when no cookie store can be found for the requested
// browser (it is not installed, or no finder is registered for it).
var ErrNoStore = errors.New("auth: no cookie store found")

// supportedBrowsers are the browser identifiers kooky's finders register under
// and that ImportFromBrowser accepts (besides "" / "auto").
var supportedBrowsers = map[string]bool{
	"chrome":   true,
	"chromium": true,
	"edge":     true,
	"brave":    true,
	"firefox":  true,
	"safari":   true,
}

// cookieOrder is the canonical emission order for the known sign-in cookies. Any
// gathered cookie not listed here is appended afterwards in alphabetical order,
// so the assembled header is fully deterministic regardless of the browser
// store's own ordering. The order is cosmetic — YouTube does not require a
// specific one — but determinism keeps the header stable and testable.
var cookieOrder = []string{
	"VISITOR_INFO1_LIVE",
	"VISITOR_PRIVACY_METADATA",
	"PREF",
	"HSID",
	"SSID",
	"APISID",
	"SAPISID",
	"__Secure-1PAPISID",
	"__Secure-3PAPISID",
	"SID",
	"__Secure-1PSID",
	"__Secure-3PSID",
	"LOGIN_INFO",
	"SIDCC",
	"__Secure-1PSIDCC",
	"__Secure-3PSIDCC",
	"__Secure-1PSIDTS",
	"__Secure-3PSIDTS",
	"YSC",
}

// AssembleCookieHeader builds the canonical "name=value; name=value" Cookie
// header from a set of browser cookies. Cookies are deduplicated by name (the
// first non-empty value wins), empty values are dropped, and names are emitted in
// a fixed canonical order (cookieOrder first, then any extras alphabetically).
//
// It returns ErrNoSAPISID when the result lacks a SAPISID/__Secure-3PAPISID
// cookie, since that cookie is required to sign InnerTube requests; this is how a
// logged-out (or undecryptable) cookie set is detected.
func AssembleCookieHeader(cookies []*http.Cookie) (string, error) {
	values := make(map[string]string)
	for _, c := range cookies {
		if c == nil {
			continue
		}
		name := strings.TrimSpace(c.Name)
		if name == "" || c.Value == "" {
			continue
		}
		if _, ok := values[name]; ok {
			continue // first non-empty value wins
		}
		values[name] = c.Value
	}

	if values["SAPISID"] == "" && values["__Secure-3PAPISID"] == "" {
		return "", ErrNoSAPISID
	}

	parts := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, name := range cookieOrder {
		if v, ok := values[name]; ok {
			parts = append(parts, name+"="+v)
			seen[name] = true
		}
	}
	extras := make([]string, 0, len(values))
	for name := range values {
		if !seen[name] {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	for _, name := range extras {
		parts = append(parts, name+"="+values[name])
	}
	return strings.Join(parts, "; "), nil
}

// storeReader is the slice of a cookie store's behaviour the profile selector
// needs: assemble the store's Cookie header (ErrNoSAPISID when it holds no
// session) and report whether it is the browser's default profile. kooky's
// CookieStore satisfies it via kookyStore; tests inject fakes.
type storeReader interface {
	header(ctx context.Context) (string, error)
	isDefaultProfile() bool
}

// kookyStore adapts a kooky.CookieStore to storeReader. It does not close the
// underlying store; ImportFromBrowser owns that (the selector may stop early).
type kookyStore struct {
	st      kooky.CookieStore
	browser string
}

func (k *kookyStore) header(ctx context.Context) (string, error) {
	return readStoreHeader(ctx, k.st, k.browser)
}

func (k *kookyStore) isDefaultProfile() bool { return k.st.IsDefaultProfile() }

// pickStoreHeader assembles a Cookie header from the first usable store among
// candidates, preferring the browser's DEFAULT profile when several profiles
// carry a session. This stops a stale secondary profile (e.g. an old, empty
// Firefox *.default beside the active *.default-release that profiles.ini marks
// Default) from winning just because kooky iterated it first.
//
// Selection: the first default-profile store that yields a SAPISID wins
// outright; otherwise the first non-default store with a SAPISID is used (so a
// session living only on a secondary profile is still found). When no store
// holds a session, a real read error (e.g. a macOS Keychain denial) is surfaced
// over the bare ErrNoSAPISID.
func pickStoreHeader(ctx context.Context, stores []storeReader) (string, error) {
	var (
		fallback     string
		haveFallback bool
		lastErr      error
	)
	for _, s := range stores {
		header, err := s.header(ctx)
		if err != nil {
			if !errors.Is(err, ErrNoSAPISID) {
				lastErr = err // a genuine read failure, not just "no session"
			}
			continue
		}
		if s.isDefaultProfile() {
			return header, nil
		}
		if !haveFallback {
			fallback, haveFallback = header, true
		}
	}
	if haveFallback {
		return fallback, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrNoSAPISID
}

// ImportFromBrowser reads the YouTube/Google sign-in cookies from the named
// browser's local cookie store and assembles them into the Cookie header tubeamp
// sends. browser is one of "chrome", "chromium", "edge", "brave", "firefox" or
// "safari"; "" or "auto" tries every supported store and returns the first that
// yields a usable (SAPISID-bearing) set.
//
// When a browser exposes several profiles (e.g. Firefox's *.default and
// *.default-release), the store whose cookie DB actually holds a session is
// chosen, preferring the profile profiles.ini marks as the default — so a stale,
// empty secondary profile never wins (see pickStoreHeader).
//
// Locked SQLite stores (a running Chrome) are handled by kooky, which reads
// through a temporary copy, so a running browser does not block the read.
// CAVEAT: that on-disk snapshot can be stale — a browser that is open keeps a
// fresh login in memory and the SQLite WAL, so a genuinely-signed-in user can
// still import cookies that resolve anonymous; quitting the browser first is the
// fix (the -auth command says so when it detects the browser is running). On
// macOS the Chrome-family stores are encrypted with a key held in the login
// Keychain: the first read raises a consent prompt, and a denial (or Chrome's
// newer app-bound encryption refusing external reads) is surfaced as a clear,
// actionable error rather than a raw decryption failure.
//
// It returns ErrNoStore when no store is found for the requested browser and
// ErrNoSAPISID (or the more informative read error) when stores were found but
// none held a signed-in session.
func ImportFromBrowser(browser string) (string, error) {
	want := strings.ToLower(strings.TrimSpace(browser))
	if want == "auto" {
		want = ""
	}
	if want != "" && !supportedBrowsers[want] {
		return "", fmt.Errorf("auth: unsupported browser %q (want chrome, chromium, edge, brave, firefox or safari)", browser)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stores := kooky.FindAllCookieStores(ctx)

	var candidates []storeReader
	for _, st := range stores {
		if st == nil {
			continue
		}
		name := strings.ToLower(st.Browser())
		if want != "" {
			if name != want {
				st.Close()
				continue
			}
		} else if !supportedBrowsers[name] {
			st.Close()
			continue
		}
		candidates = append(candidates, &kookyStore{st: st, browser: name})
	}
	// Close every store we kept once selection is done (pickStoreHeader may stop
	// before reading them all).
	defer func() {
		for _, c := range candidates {
			if ks, ok := c.(*kookyStore); ok {
				ks.st.Close()
			}
		}
	}()

	if len(candidates) == 0 {
		if want != "" {
			return "", fmt.Errorf("%w for %q (is it installed?)", ErrNoStore, want)
		}
		return "", fmt.Errorf("%w: no supported browser detected", ErrNoStore)
	}
	return pickStoreHeader(ctx, candidates)
}

// readStoreHeader reads one cookie store, keeps only the YouTube/Google cookies,
// and assembles the header. It does NOT close the store (the caller owns that).
// A decryption failure on a Chrome-family store (a macOS Keychain denial, or
// Chrome's app-bound encryption) is wrapped with an actionable hint.
func readStoreHeader(ctx context.Context, st kooky.CookieStore, browser string) (string, error) {
	all, readErr := st.TraverseCookies().ReadAllCookies(ctx)

	cookies := make([]*http.Cookie, 0, len(all))
	for _, c := range all {
		if c == nil || !relevantDomain(c.Domain) {
			continue
		}
		hc := c.Cookie // copy out the embedded http.Cookie; decouples us from kooky
		cookies = append(cookies, &hc)
	}

	header, err := AssembleCookieHeader(cookies)
	if err != nil {
		// When the assembly failed because nothing decrypted, the read error is
		// the more informative one to surface (e.g. a Keychain denial leaves us
		// with no cookies at all).
		if readErr != nil {
			return "", decorateReadErr(browser, readErr)
		}
		return "", err
	}
	return header, nil
}

// relevantDomain reports whether a cookie's domain belongs to YouTube or Google
// (music.youtube.com / .youtube.com / .google.com and subdomains).
func relevantDomain(domain string) bool {
	d := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "."))
	switch {
	case d == "youtube.com" || strings.HasSuffix(d, ".youtube.com"):
		return true
	case d == "google.com" || strings.HasSuffix(d, ".google.com"):
		return true
	default:
		return false
	}
}

// decorateReadErr wraps a cookie-store read error, adding macOS-specific guidance
// for the Chrome family where decryption needs Keychain access.
func decorateReadErr(browser string, err error) error {
	if runtime.GOOS == "darwin" && isChromeFamily(browser) {
		return fmt.Errorf("auth: could not read %s cookies — on macOS tubeamp needs Keychain access to decrypt them (allow the prompt); very recent Chrome may refuse external reads (app-bound encryption), in which case use Firefox or the manual cookie method: %w", browser, err)
	}
	return fmt.Errorf("auth: could not read %s cookies: %w", browser, err)
}

func isChromeFamily(browser string) bool {
	switch browser {
	case "chrome", "chromium", "edge", "brave":
		return true
	default:
		return false
	}
}
