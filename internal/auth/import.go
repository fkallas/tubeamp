// Package auth imports a YouTube Music sign-in from a local browser profile.
//
// Instead of hand-copying the Cookie header out of devtools (see the manual
// method in the README), tubeamp reads the YouTube/Google sign-in cookies
// straight out of a browser via yt-dlp's --cookies-from-browser extractor and
// assembles the canonical Cookie header tubeamp's InnerTube client sends. This
// backs both `tubeamp -auth <browser>` and the UI's one-shot stale-session
// auto-refresh.
//
// yt-dlp (already a hard tubeamp dependency, for playback) is used as the reader
// rather than a native cookie-store library because it handles every awkward
// case correctly: a RUNNING browser (it reads the live SQLite WAL, not just the
// stale on-disk snapshot a naive reader sees), profile selection, and the full
// Chrome-family decryption (macOS Keychain, Linux keyrings, app-bound
// encryption). An on-disk snapshot read by a naive library while the browser is
// open yields cookies that resolve anonymous even for a signed-in user; yt-dlp
// avoids that, which is why it is the reader here.
//
// AssembleCookieHeader (the pure, fixture-tested core) is split from
// ImportFromBrowser (the yt-dlp-backed reader) so the header assembly and the
// Netscape-jar parsing can be unit-tested without a real browser.
package auth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNoSAPISID is returned when an assembled cookie set lacks the SAPISID (or
// __Secure-3PAPISID) cookie the SAPISIDHASH authorization depends on — i.e. the
// browser is not signed into a Google account. It is the typed "no usable cookie
// set" error callers match with errors.Is.
var ErrNoSAPISID = errors.New("auth: no SAPISID cookie found (is the browser signed into YouTube?)")

// ErrNoStore is returned when no usable cookie set can be extracted from the
// requested browser (it is not installed, or yt-dlp could not read it).
var ErrNoStore = errors.New("auth: no usable browser cookies found")

// ErrYTDLPMissing is returned when the yt-dlp binary is not on PATH. yt-dlp is a
// hard tubeamp dependency (it also resolves playback streams), so this normally
// cannot happen, but the import surfaces it cleanly.
var ErrYTDLPMissing = errors.New("auth: yt-dlp not found on PATH (required to read browser cookies; install it: brew install yt-dlp)")

// supportedBrowsers are the browser identifiers ImportFromBrowser accepts
// (besides "" / "auto"). They match yt-dlp's --cookies-from-browser names.
var supportedBrowsers = map[string]bool{
	"chrome":   true,
	"chromium": true,
	"edge":     true,
	"brave":    true,
	"firefox":  true,
	"safari":   true,
	"opera":    true,
	"vivaldi":  true,
}

// autoOrder is the sequence the "auto" import tries browsers in: the popular
// ones first. The first browser that yields a signed-in (SAPISID-bearing) cookie
// set wins.
var autoOrder = []string{"firefox", "chrome", "brave", "edge", "chromium", "vivaldi", "opera", "safari"}

// cookieOrder is the canonical emission order for the known sign-in cookies. Any
// gathered cookie not listed here is appended afterwards in alphabetical order,
// so the assembled header is fully deterministic regardless of the jar's own
// ordering. The order is cosmetic — YouTube does not require a specific one — but
// determinism keeps the header stable and testable.
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
// logged-out cookie set is detected.
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

// parseNetscapeCookies parses a Netscape-format cookie jar (the format yt-dlp
// writes) into http.Cookies, keeping only the YouTube/Google sign-in cookies.
// Each data line is seven tab-separated fields:
//
//	domain  includeSubdomains  path  secure  expiry  name  value
//
// Comment lines start with '#', EXCEPT yt-dlp/curl encode an HttpOnly cookie by
// prefixing the domain with "#HttpOnly_" — those are real cookies, not comments.
func parseNetscapeCookies(r *bufio.Scanner) []*http.Cookie {
	var cookies []*http.Cookie
	for r.Scan() {
		line := r.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if !strings.HasPrefix(line, "#HttpOnly_") {
				continue // a genuine comment
			}
			line = strings.TrimPrefix(line, "#HttpOnly_")
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		domain, name, value := f[0], f[5], f[6]
		if !relevantDomain(domain) {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Domain: domain})
	}
	return cookies
}

// ImportFromBrowser reads the YouTube/Google sign-in cookies from the named
// browser via yt-dlp and assembles them into the Cookie header tubeamp sends.
// browser is one of the supportedBrowsers ids; "" or "auto" tries each browser
// in autoOrder and returns the first that yields a usable (SAPISID-bearing) set.
// sourceBrowser names the browser that actually supplied the header (lowercase
// id, never "auto"; "" on error), so callers on the auto path can attribute the
// session and remember a concrete re-import source.
//
// Because the read goes through yt-dlp, a running browser is handled correctly
// (the live cookies are read, not a stale snapshot) and Chrome-family
// decryption "just works" (on macOS the first read may raise a Keychain consent
// prompt). It returns ErrYTDLPMissing when yt-dlp is absent, ErrNoStore when the
// requested browser yields nothing, and ErrNoSAPISID when cookies were read but
// none carried a signed-in session.
func ImportFromBrowser(browser string) (cookieHeader, sourceBrowser string, err error) {
	want := strings.ToLower(strings.TrimSpace(browser))
	if want == "auto" {
		want = ""
	}
	if want != "" && !supportedBrowsers[want] {
		return "", "", fmt.Errorf("auth: unsupported browser %q (want chrome, chromium, edge, brave, firefox, safari, opera or vivaldi)", browser)
	}
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return "", "", ErrYTDLPMissing
	}

	order := autoOrder
	if want != "" {
		order = []string{want}
	}

	var lastErr error
	for _, b := range order {
		header, err := importViaYTDLP(b)
		if err == nil {
			return header, b, nil
		}
		// For an explicit single browser, surface its error directly. For "auto",
		// keep the most informative error (a real read failure over a bare
		// "no session") and keep trying the next browser.
		if want != "" {
			return "", "", err
		}
		if lastErr == nil || errors.Is(lastErr, ErrNoSAPISID) {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no supported browser had a YouTube session", ErrNoStore)
	}
	return "", "", lastErr
}

// importViaYTDLP runs yt-dlp's cookie extractor for one browser into a temp jar
// and assembles the header. yt-dlp needs a URL to act on; the YTM homepage with
// --playlist-items 0 makes it load+write the jar with minimal work. yt-dlp exits
// non-zero when there is nothing to download, so the exit code is ignored — the
// written jar (or its absence) is the real signal.
func importViaYTDLP(browser string) (string, error) {
	// yt-dlp treats --cookies as BOTH an input and an output jar: it tries to
	// LOAD the path first and rejects an empty/invalid file. So hand it a path
	// that does not exist yet (inside a temp dir we own) and let it create the
	// jar for output.
	dir, err := os.MkdirTemp("", "tubeamp-cookies-")
	if err != nil {
		return "", fmt.Errorf("auth: temp cookie dir: %w", err)
	}
	defer os.RemoveAll(dir)
	tmpPath := filepath.Join(dir, "cookies.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--cookies-from-browser", browser,
		"--cookies", tmpPath,
		"--simulate", "--skip-download", "--playlist-items", "0",
		"--no-warnings",
		"https://music.youtube.com/",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // exit code ignored; the jar is the signal

	f, err := os.Open(tmpPath)
	if err != nil {
		return "", browserReadErr(browser, &stderr)
	}
	defer f.Close()
	cookies := parseNetscapeCookies(bufio.NewScanner(f))

	header, err := AssembleCookieHeader(cookies)
	if err != nil {
		if errors.Is(err, ErrNoSAPISID) && stderr.Len() > 0 {
			// yt-dlp wrote no usable cookies AND complained — its message
			// (e.g. "could not find chrome cookies database", a Keychain denial)
			// is the more actionable one to surface.
			return "", browserReadErr(browser, &stderr)
		}
		return "", err
	}
	return header, nil
}

// browserReadErr builds an actionable error from a failed/empty yt-dlp read,
// folding in yt-dlp's own stderr tail when present.
func browserReadErr(browser string, stderr *bytes.Buffer) error {
	msg := strings.TrimSpace(stderr.String())
	if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:]) // last line is usually the real error
	}
	if msg == "" {
		return fmt.Errorf("%w: yt-dlp read no cookies from %s (is it installed and signed in?)", ErrNoStore, browser)
	}
	return fmt.Errorf("auth: could not read %s cookies via yt-dlp: %s", browser, msg)
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
