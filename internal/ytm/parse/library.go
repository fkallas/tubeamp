package parse

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fkallas/tubeamp/internal/model"
)

// ErrNotSignedIn reports that an InnerTube library/playlist browse came back as
// the logged-out page rather than a real, possibly empty, library result. These
// browses require a live signed-in session, so a missing or stale (anonymous)
// cookie surfaces here. Returning a typed error (instead of an empty success)
// lets the UI tell "your library is empty" apart from "you are not signed in".
// Callers should match it with errors.Is.
//
// Detection is positive where possible: every InnerTube response stamps a
// responseContext.serviceTrackingParams param {"key":"logged_in","value":"0"|"1"}
// (verified against live anonymous captures), so "0" means logged out and "1"
// with the expected renderer absent means a signed-in-but-empty page. Only when
// the marker is missing does renderer absence alone imply the logged-out page.
var ErrNotSignedIn = errors.New("ytm: not signed in")

// signInState reads the positive sign-in marker InnerTube stamps on every
// response: the responseContext.serviceTrackingParams param with key
// "logged_in" ("0" anonymous, "1" signed in). known is false when the marker is
// absent (unexpected shape), in which case callers fall back to
// renderer-presence heuristics.
func signInState(root map[string]any) (signedIn, known bool) {
	for _, stp := range getSlice(getPath(root, "responseContext", "serviceTrackingParams")) {
		for _, p := range getSlice(getPath(asMap(stp), "params")) {
			pm := asMap(p)
			if str(getPath(pm, "key")) == "logged_in" {
				return str(getPath(pm, "value")) == "1", true
			}
		}
	}
	return false, false
}

// LibraryPlaylists parses a browse of FEmusic_liked_playlists (the "your
// playlists" grid) into the user's playlists. The grid's first tile is the
// synthetic "New playlist" create button and is skipped, as are non-playlist
// tiles (no playlist browseId, or a non-playlist pageType). Each playlist's id
// has its leading "VL" stripped, and the track count is read from the subtitle
// ("23 songs") when present. A logged-out page (responseContext logged_in "0",
// or no marker and no gridRenderer) returns ErrNotSignedIn; a signed-in page
// without a grid is an empty library and returns an empty result. Malformed
// items are skipped; the function never panics.
func LibraryPlaylists(raw []byte) ([]model.Playlist, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse.LibraryPlaylists: %w", err)
	}

	// This browse is inherently per-account: an anonymous session never gets a
	// real grid (live captures show a sign-in messageRenderer instead), so the
	// logged-out marker wins even over a stray grid elsewhere in the response.
	signedIn, known := signInState(root)
	if known && !signedIn {
		return nil, ErrNotSignedIn
	}

	// The playlist grid lives under a gridRenderer somewhere in the
	// single-column browse result; a deep search tolerates layout shuffles.
	grids := findAll(root, "gridRenderer")
	if len(grids) == 0 {
		if known { // signed in (the marker said so) but no grid: empty library
			return nil, nil
		}
		return nil, ErrNotSignedIn
	}
	var items []any
	for _, g := range grids {
		if its := getSlice(getPath(g, "items")); len(its) > 0 {
			items = its
			break
		}
	}

	var playlists []model.Playlist
	for _, it := range items {
		r := asMap(getPath(asMap(it), "musicTwoRowItemRenderer"))
		if r == nil {
			continue
		}
		pl, ok := parseLibraryPlaylist(r)
		if !ok {
			continue
		}
		playlists = append(playlists, pl)
	}
	return playlists, nil
}

// parseLibraryPlaylist extracts one playlist from a musicTwoRowItemRenderer.
// Returns ok=false for the synthetic "New playlist" tile and for any tile that
// is not a playlist: no browseId, a pageType other than
// MUSIC_PAGE_TYPE_PLAYLIST, or — when the pageType is absent — a browseId
// without the VL/PL playlist prefix (album/artist/promo tiles must not parse as
// playlists).
func parseLibraryPlaylist(r map[string]any) (model.Playlist, bool) {
	title := str(getPath(r, "title", "runs", "0", "text"))
	if title == "" || title == "New playlist" {
		// The create tile (and any title-less malformed item) is not a playlist.
		return model.Playlist{}, false
	}
	endpoint := asMap(getPath(r, "title", "runs", "0",
		"navigationEndpoint", "browseEndpoint"))
	browseID := str(getPath(endpoint, "browseId"))
	if browseID == "" {
		return model.Playlist{}, false
	}
	pageType := str(getPath(endpoint,
		"browseEndpointContextSupportedConfigs",
		"browseEndpointContextMusicConfig", "pageType"))
	switch {
	case pageType == "MUSIC_PAGE_TYPE_PLAYLIST":
	case pageType == "" && (strings.HasPrefix(browseID, "VL") || strings.HasPrefix(browseID, "PL")):
		// Untyped tile with a playlist-shaped id: accept defensively.
	default:
		return model.Playlist{}, false
	}
	return model.Playlist{
		ID:         strings.TrimPrefix(browseID, "VL"),
		Title:      title,
		TrackCount: parsePlaylistCount(getSlice(getPath(r, "subtitle", "runs"))),
	}, true
}

// parsePlaylistCount scans subtitle runs ("Playlist • 23 songs") for the first
// "<n> songs"/"<n> tracks" run and returns n; 0 when no count is present.
func parsePlaylistCount(runs []any) int {
	for _, run := range runs {
		fields := strings.Fields(str(getPath(asMap(run), "text")))
		if len(fields) < 2 {
			continue
		}
		unit := strings.ToLower(fields[1])
		if !strings.HasPrefix(unit, "song") && !strings.HasPrefix(unit, "track") {
			continue
		}
		if n, err := strconv.Atoi(strings.ReplaceAll(fields[0], ",", "")); err == nil {
			return n
		}
	}
	return 0
}

// PlaylistTracks parses a browse of a playlist page (browseId "VL"+playlistId, or
// "VLLM" for the Liked Songs auto-playlist) into its tracks. A playlist page is a
// musicPlaylistShelfRenderer of musicResponsiveListItemRenderer rows, reused here
// for both Liked Songs and ordinary playlists. A present shelf always parses
// (public playlists browse fine anonymously); an empty-but-present shelf yields
// an empty slice. When no shelf is found, the responseContext logged_in marker
// discriminates: a signed-in page without a shelf is an empty playlist (empty
// slice), anything else is the logged-out page and returns ErrNotSignedIn.
// Malformed rows are skipped; the function never panics.
//
// TODO(continuation): only the first page (~100 rows) is returned. Follow the
// shelf's musicPlaylistShelfContinuation token to load longer playlists.
func PlaylistTracks(raw []byte) ([]model.Track, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse.PlaylistTracks: %w", err)
	}

	shelves := findAll(root, "musicPlaylistShelfRenderer")
	if len(shelves) == 0 {
		if signedIn, known := signInState(root); known && signedIn {
			// Signed in but no shelf rendered: an empty playlist page, not a
			// sign-in problem.
			return nil, nil
		}
		return nil, ErrNotSignedIn
	}

	var tracks []model.Track
	for _, shelf := range shelves {
		for _, item := range getSlice(getPath(shelf, "contents")) {
			r := asMap(getPath(asMap(item), "musicResponsiveListItemRenderer"))
			if r == nil {
				continue
			}
			if t, ok := parseItem(r); ok {
				tracks = append(tracks, t)
			}
		}
		if len(tracks) > 0 {
			break
		}
	}
	return tracks, nil
}
