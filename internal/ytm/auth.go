// Package ytm is an InnerTube client for YouTube Music.
package ytm

import (
	"crypto/sha1"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Auth holds the cookies used to authenticate InnerTube requests and keeps them
// alive across Google's rotation. Google attaches Set-Cookie headers to many
// responses (SIDCC, __Secure-1PSIDCC, occasionally __Secure-*PSIDTS); Auth
// absorbs those refreshes so a healthy session does not decay the moment the
// auth file is copied.
//
// Internally Auth maintains an ordered, mutex-guarded map of the live cookie set.
// SAPISID and Header read from that live set, so a rotated value takes effect for
// subsequent requests in the same process; when the set changes it is written
// back atomically to the source file (when one is known) and a generation counter
// is bumped so the UI can notice the rotation on its next auth re-check.
//
// Auth must not be copied after first use; pass it by pointer.
type Auth struct {
	// Cookie is the raw Cookie header value as originally loaded. It is a
	// snapshot of the initial state; the live (possibly rotated) cookie set is
	// maintained internally — use Header() for the value to actually send.
	Cookie string

	mu      sync.Mutex
	order   []string                             // cookie names in send order (load order; new names appended)
	jar     map[string]string                    // live merged name->value set
	path    string                               // source file for write-back; "" disables persistence
	gen     uint64                               // bumped whenever the live cookie set changes
	writeFn func(path string, data []byte) error // injectable persistence (nil => atomicWriteFile)
}

// LoadAuth reads the plain-text auth file at path (one line, the Cookie header
// value), trims whitespace, and returns an Auth bound to that path so rotated
// cookies are persisted back. Returns an error if the file is missing or the
// trimmed content is empty.
func LoadAuth(path string) (*Auth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ytm.LoadAuth: %w", err)
	}
	cookie := strings.TrimSpace(string(data))
	if cookie == "" {
		return nil, fmt.Errorf("ytm.LoadAuth: auth file %q is empty", path)
	}
	a := &Auth{Cookie: cookie, path: path}
	a.order, a.jar = parseCookieHeader(cookie)
	return a, nil
}

// initLocked lazily builds the live cookie set from Cookie. It must be called
// with a.mu held. It makes directly-constructed Auths (e.g. &Auth{Cookie: ...})
// behave like LoadAuth'd ones on first use, minus the write-back path.
func (a *Auth) initLocked() {
	if a.jar != nil {
		return
	}
	a.order, a.jar = parseCookieHeader(a.Cookie)
}

// SAPISID returns the SAPISID value from the live cookie set. It prefers the
// "SAPISID" cookie and falls back to "__Secure-3PAPISID". A rotated value (rare,
// but possible) takes effect here. Returns an error if neither is present.
func (a *Auth) SAPISID() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()
	if v, ok := a.jar["SAPISID"]; ok {
		return v, nil
	}
	if v, ok := a.jar["__Secure-3PAPISID"]; ok {
		return v, nil
	}
	return "", fmt.Errorf("ytm: no SAPISID or __Secure-3PAPISID found in cookie")
}

// Header returns the current Cookie header value to send, serialized from the
// live cookie set in canonical "name=value; name=value" order.
func (a *Auth) Header() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()
	return serializeCookies(a.order, a.jar)
}

// Generation returns a counter that increases every time the live cookie set
// changes (a rotation absorbed from a Set-Cookie response). The UI can poll it on
// its existing auth re-check to notice that the session was refreshed without any
// goroutine or channel on Auth.
func (a *Auth) Generation() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen
}

// merge folds the cookies from a response's Set-Cookie headers into the live set:
// new/changed values win, and Max-Age=0 / expired cookies drop their name.
// Unchanged entries keep their original order; genuinely new names are appended.
// When anything changed it bumps the generation counter and persists the set.
func (a *Auth) merge(cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	now := time.Now()
	changed := false
	for _, c := range cookies {
		if c == nil || c.Name == "" {
			continue
		}
		if cookieDeleted(c, now) {
			if _, ok := a.jar[c.Name]; ok {
				delete(a.jar, c.Name)
				a.removeFromOrderLocked(c.Name)
				changed = true
			}
			continue
		}
		if cur, ok := a.jar[c.Name]; ok {
			if cur != c.Value {
				a.jar[c.Name] = c.Value
				changed = true
			}
			continue
		}
		a.jar[c.Name] = c.Value
		a.order = append(a.order, c.Name)
		changed = true
	}
	if !changed {
		return
	}
	a.gen++
	a.persistLocked()
}

// removeFromOrderLocked drops name from the send order. a.mu must be held.
func (a *Auth) removeFromOrderLocked(name string) {
	for i, n := range a.order {
		if n == name {
			a.order = append(a.order[:i], a.order[i+1:]...)
			return
		}
	}
}

// persistLocked writes the serialized live set back to the source file. a.mu must
// be held. An Auth with no path only updates memory. Persistence is best-effort:
// a write failure leaves the in-memory set live and does not break the request.
func (a *Auth) persistLocked() {
	if a.path == "" {
		return
	}
	data := []byte(serializeCookies(a.order, a.jar) + "\n")
	write := a.writeFn
	if write == nil {
		write = atomicWriteFile
	}
	_ = write(a.path, data)
}

// cookieDeleted reports whether a Set-Cookie directs us to drop the cookie:
// Max-Age=0/negative (net/http normalizes both to MaxAge<0) or an Expires in the
// past.
func cookieDeleted(c *http.Cookie, now time.Time) bool {
	if c.MaxAge < 0 {
		return true
	}
	if !c.Expires.IsZero() && !c.Expires.After(now) {
		return true
	}
	return false
}

// parseCookieHeader splits a "name=value; name=value" Cookie header into the
// send order and a name->value map. Values may themselves contain '=' (only the
// first '=' separates name from value); blank or nameless segments are skipped.
func parseCookieHeader(header string) (order []string, jar map[string]string) {
	jar = make(map[string]string)
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := jar[name]; !exists {
			order = append(order, name)
		}
		jar[name] = value
	}
	return order, jar
}

// serializeCookies renders the live set back to a canonical Cookie header,
// emitting names in order and skipping any that have been dropped.
func serializeCookies(order []string, jar map[string]string) string {
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if v, ok := jar[name]; ok {
			parts = append(parts, name+"="+v)
		}
	}
	return strings.Join(parts, "; ")
}

// atomicWriteFile writes data to path via a temp file in the same directory and a
// rename, so a concurrent reader never sees a half-written auth file. The file is
// created 0600 (it holds a credential).
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tubeamp-auth-*")
	if err != nil {
		return fmt.Errorf("ytm: create temp auth file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("ytm: chmod temp auth file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ytm: write temp auth file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ytm: close temp auth file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ytm: rename temp auth file: %w", err)
	}
	return nil
}

// sapisidHash computes the SAPISIDHASH authorization token.
//
// Format: "SAPISIDHASH <unixts>_<sha1hex of '<unixts> <sapisid> <origin>'>"
func sapisidHash(sapisid, origin string, now time.Time) string {
	ts := fmt.Sprintf("%d", now.Unix())
	h := sha1.New()
	h.Write([]byte(ts + " " + sapisid + " " + origin))
	return fmt.Sprintf("SAPISIDHASH %s_%x", ts, h.Sum(nil))
}
