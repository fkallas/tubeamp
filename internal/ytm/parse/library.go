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
// the logged-out page — the expected renderer (a gridRenderer for the playlist
// grid, a musicPlaylistShelfRenderer for a playlist's tracks) is absent — rather
// than a real, possibly empty, library result. These browses require a live
// signed-in session, so a missing or stale (anonymous) cookie surfaces here.
// Returning a typed error (instead of an empty success) lets the UI tell "your
// library is empty" apart from "you are not signed in". Callers should match it
// with errors.Is.
var ErrNotSignedIn = errors.New("ytm: not signed in")

// LibraryPlaylists parses a browse of FEmusic_liked_playlists (the "your
// playlists" grid) into the user's playlists. The grid's first tile is the
// synthetic "New playlist" create button and is skipped, as is any item without
// a playlist browseId. Each playlist's id has its leading "VL" stripped, and the
// track count is read from the subtitle ("23 songs") when present. When no
// gridRenderer is found the page is the logged-out one, so ErrNotSignedIn is
// returned. Malformed items are skipped; the function never panics.
func LibraryPlaylists(raw []byte) ([]model.Playlist, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse.LibraryPlaylists: %w", err)
	}

	// The playlist grid lives under a gridRenderer somewhere in the
	// single-column browse result; a deep search tolerates layout shuffles.
	grids := findAll(root, "gridRenderer")
	if len(grids) == 0 {
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
// Returns ok=false for the synthetic "New playlist" tile and for any item whose
// title carries no playlist browseId.
func parseLibraryPlaylist(r map[string]any) (model.Playlist, bool) {
	title := str(getPath(r, "title", "runs", "0", "text"))
	if title == "" || title == "New playlist" {
		// The create tile (and any title-less malformed item) is not a playlist.
		return model.Playlist{}, false
	}
	browseID := str(getPath(r, "title", "runs", "0",
		"navigationEndpoint", "browseEndpoint", "browseId"))
	if browseID == "" {
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
// for both Liked Songs and ordinary playlists. When no shelf is found the page is
// the logged-out one, so ErrNotSignedIn is returned; an empty-but-present shelf
// yields an empty slice (a valid, signed-in empty playlist). Malformed rows are
// skipped; the function never panics.
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
