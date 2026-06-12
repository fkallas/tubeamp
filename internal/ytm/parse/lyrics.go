package parse

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNoLyrics reports that a YouTube Music response carried no lyrics: either
// the watch (next) response has no lyrics tab, or the lyrics browse page has no
// description shelf (a track with no lyrics on file). It is a typed result, not
// a parse failure, so callers can match it with errors.Is and fall back.
var ErrNoLyrics = errors.New("ytm: no lyrics")

// LyricsBrowseID finds the lyrics-tab browseId in a `next` (watch) response.
// YouTube Music's watch-next tabs include a "Lyrics" tab whose
// browseEndpoint.browseId (an "MPLYt…" id) is the lyrics browse target. The
// tabs are found by a deep search so layout shuffles do not break discovery.
// Returns ErrNoLyrics when no lyrics tab is present; only malformed JSON
// returns a wrapped error.
//
// TODO: verify against ytmusicapi — the tab title ("Lyrics") and the "MPLYt"
// browseId prefix are taken from observed responses, not a documented contract.
func LyricsBrowseID(raw []byte) (string, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("parse.LyricsBrowseID: %w", err)
	}

	for _, tab := range findAll(root, "tabRenderer") {
		browseID := str(getPath(tab, "endpoint", "browseEndpoint", "browseId"))
		if browseID == "" {
			continue
		}
		title := str(getPath(tab, "title"))
		if strings.EqualFold(title, "Lyrics") || strings.HasPrefix(browseID, "MPLYt") {
			return browseID, nil
		}
	}
	return "", ErrNoLyrics
}

// LyricsText extracts the plain lyrics from a lyrics browse response. The text
// lives in a musicDescriptionShelfRenderer's description (run-list or
// simpleText). Returns ErrNoLyrics when no non-empty description shelf is
// found; only malformed JSON returns a wrapped error. Defensive throughout:
// missing keys are skipped, never panicking.
func LyricsText(raw []byte) (string, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("parse.LyricsText: %w", err)
	}

	for _, shelf := range findAll(root, "musicDescriptionShelfRenderer") {
		desc := asMap(getPath(shelf, "description"))
		if desc == nil {
			continue
		}
		if text := runsText(getPath(desc, "runs")); strings.TrimSpace(text) != "" {
			return text, nil
		}
		if text := str(getPath(desc, "simpleText")); strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return "", ErrNoLyrics
}
