package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNoLyrics reports that no lyrics could be found for the requested track.
// Callers should match it with errors.Is to tell "no lyrics" apart from a
// network or decode failure.
var ErrNoLyrics = errors.New("lyrics: no lyrics found")

// userAgent identifies tubeamp to LRCLIB, as their API etiquette requests.
const userAgent = "tubeamp (https://github.com/fkallas/tubeamp)"

// maxBody caps a single LRCLIB response read (~1 MiB) to bound allocations.
const maxBody = 1 << 20

// Doer is the subset of *http.Client used here. It is a package-level var so
// tests can inject an httptest-backed client (or a stub) without touching the
// network.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// httpClient performs LRCLIB requests; overridable in tests.
var httpClient Doer = &http.Client{Timeout: 10 * time.Second}

// baseURL is the LRCLIB API root; overridable in tests to point at httptest.
var baseURL = "https://lrclib.net"

// lrclibTrack is the relevant subset of an LRCLIB track object (returned by
// both /api/get and as elements of /api/search).
type lrclibTrack struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
}

// FetchLRCLIB looks up lyrics on LRCLIB for the given track.
//
// It first tries the exact-match endpoint GET /api/get with the supplied
// artist/title/album and duration (seconds). On 200 it prefers syncedLyrics
// (parsed as LRC, Synced=true) and falls back to plainLyrics (Synced=false).
// On 404 it retries via GET /api/search?q=<artist title>, taking the first hit
// that carries synced lyrics, else the first hit that carries plain lyrics.
// When nothing usable is found it returns ErrNoLyrics; network, HTTP and decode
// failures are returned wrapped. The context bounds both requests.
func FetchLRCLIB(ctx context.Context, artist, title, album string, dur time.Duration) (Lyrics, error) {
	q := url.Values{}
	q.Set("artist_name", artist)
	q.Set("track_name", title)
	if album != "" {
		q.Set("album_name", album)
	}
	if dur > 0 {
		// LRCLIB matches on integer seconds; round to nearest.
		secs := int((dur + time.Second/2) / time.Second)
		q.Set("duration", strconv.Itoa(secs))
	}

	body, status, err := doGet(ctx, baseURL+"/api/get?"+q.Encode())
	if err != nil {
		return Lyrics{}, err
	}
	switch {
	case status == http.StatusOK:
		var tr lrclibTrack
		if err := json.Unmarshal(body, &tr); err != nil {
			return Lyrics{}, fmt.Errorf("lyrics: decode get response: %w", err)
		}
		if ly, ok := lyricsFrom(tr.SyncedLyrics, tr.PlainLyrics); ok {
			return ly, nil
		}
		return Lyrics{}, ErrNoLyrics // instrumental / empty body
	case status == http.StatusNotFound:
		// fall through to the search endpoint
	default:
		return Lyrics{}, fmt.Errorf("lyrics: lrclib get: HTTP %d", status)
	}

	return searchLRCLIB(ctx, artist, title)
}

// searchLRCLIB performs the /api/search fallback and selects a hit.
func searchLRCLIB(ctx context.Context, artist, title string) (Lyrics, error) {
	sq := url.Values{}
	sq.Set("q", strings.TrimSpace(artist+" "+title))

	body, status, err := doGet(ctx, baseURL+"/api/search?"+sq.Encode())
	if err != nil {
		return Lyrics{}, err
	}
	if status != http.StatusOK {
		return Lyrics{}, fmt.Errorf("lyrics: lrclib search: HTTP %d", status)
	}

	var hits []lrclibTrack
	if err := json.Unmarshal(body, &hits); err != nil {
		return Lyrics{}, fmt.Errorf("lyrics: decode search response: %w", err)
	}

	// Prefer the first hit with usable synced lyrics.
	for _, h := range hits {
		if strings.TrimSpace(h.SyncedLyrics) == "" {
			continue
		}
		if ly, ok := lyricsFrom(h.SyncedLyrics, h.PlainLyrics); ok {
			return ly, nil
		}
	}
	// Otherwise the first hit that carries plain lyrics.
	for _, h := range hits {
		if ly, ok := lyricsFrom("", h.PlainLyrics); ok {
			return ly, nil
		}
	}
	return Lyrics{}, ErrNoLyrics
}

// lyricsFrom builds a Lyrics from an LRCLIB synced/plain pair, preferring synced
// when it parses to at least one line. Returns ok=false when neither yields
// content (so the caller can fall back or report ErrNoLyrics).
func lyricsFrom(synced, plain string) (Lyrics, bool) {
	if strings.TrimSpace(synced) != "" {
		if lines := ParseLRC(synced); len(lines) > 0 {
			return Lyrics{Lines: lines, Synced: true, Plain: plain, Source: SourceLRCLIB}, true
		}
	}
	if strings.TrimSpace(plain) != "" {
		return Lyrics{Plain: plain, Synced: false, Source: SourceLRCLIB}, true
	}
	return Lyrics{}, false
}

// doGet issues a GET with the tubeamp User-Agent and returns the (size-limited)
// body and HTTP status. Transport and read errors are returned wrapped.
func doGet(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("lyrics: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("lyrics: lrclib request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("lyrics: read lrclib response: %w", err)
	}
	return body, resp.StatusCode, nil
}
