// Package ytm is an InnerTube client for YouTube Music.
package ytm

import (
	"crypto/sha1"
	"fmt"
	"os"
	"strings"
	"time"
)

// Auth holds a raw Cookie header value used to authenticate InnerTube requests.
type Auth struct {
	Cookie string // raw Cookie header value
}

// LoadAuth reads the plain-text auth file at path (one line, the Cookie header
// value), trims whitespace, and returns an Auth. Returns an error if the file
// is missing or the trimmed content is empty.
func LoadAuth(path string) (*Auth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ytm.LoadAuth: %w", err)
	}
	cookie := strings.TrimSpace(string(data))
	if cookie == "" {
		return nil, fmt.Errorf("ytm.LoadAuth: auth file %q is empty", path)
	}
	return &Auth{Cookie: cookie}, nil
}

// SAPISID parses the SAPISID value from the Cookie header. It prefers the
// "SAPISID" cookie and falls back to "__Secure-3PAPISID". Returns an error if
// neither is present.
func (a *Auth) SAPISID() (string, error) {
	var fallback string
	for _, part := range strings.Split(a.Cookie, "; ") {
		part = strings.TrimSpace(part)
		if val, ok := strings.CutPrefix(part, "SAPISID="); ok {
			return val, nil
		}
		if val, ok := strings.CutPrefix(part, "__Secure-3PAPISID="); ok {
			fallback = val
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("ytm: no SAPISID or __Secure-3PAPISID found in cookie")
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
