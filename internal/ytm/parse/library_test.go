package parse

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestLibraryPlaylists_fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/library_playlists.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	pls, err := LibraryPlaylists(data)
	if err != nil {
		t.Fatalf("LibraryPlaylists: %v", err)
	}

	// 4 grid items: the synthetic "New playlist" tile and one title-less
	// malformed item are skipped, leaving two real playlists.
	if len(pls) != 2 {
		t.Fatalf("got %d playlists, want 2 (New playlist + malformed skipped)", len(pls))
	}

	// Playlist 0: VL-prefixed browseId, count from "23 songs".
	if pls[0].ID != "PLfocus123" {
		t.Errorf("pls[0].ID = %q, want %q (VL prefix stripped)", pls[0].ID, "PLfocus123")
	}
	if pls[0].Title != "Focus Deep Work" {
		t.Errorf("pls[0].Title = %q, want %q", pls[0].Title, "Focus Deep Work")
	}
	if pls[0].TrackCount != 23 {
		t.Errorf("pls[0].TrackCount = %d, want 23", pls[0].TrackCount)
	}

	// Playlist 1: id without a VL prefix is kept as-is, count absent => 0.
	if pls[1].ID != "PLgym2026" {
		t.Errorf("pls[1].ID = %q, want %q", pls[1].ID, "PLgym2026")
	}
	if pls[1].Title != "Gym 2026" {
		t.Errorf("pls[1].Title = %q, want %q", pls[1].Title, "Gym 2026")
	}
	if pls[1].TrackCount != 0 {
		t.Errorf("pls[1].TrackCount = %d, want 0 (no count run)", pls[1].TrackCount)
	}
}

func TestLibraryPlaylists_notSignedIn(t *testing.T) {
	// No logged_in marker and no gridRenderer => ErrNotSignedIn (not empty success).
	_, err := LibraryPlaylists([]byte(`{"contents":{}}`))
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
}

func TestLibraryPlaylists_loggedOutFixture(t *testing.T) {
	// Trimmed live anonymous capture: logged_in "0" marker plus a sign-in
	// messageRenderer instead of the playlist grid.
	data, err := os.ReadFile("testdata/library_playlists_logged_out.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, err = LibraryPlaylists(data)
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
}

func TestLibraryPlaylists_loggedOutMarkerWinsOverStrayGrid(t *testing.T) {
	// The library browse is per-account: with the logged_in "0" marker, even a
	// response carrying some unrelated grid must classify as not-signed-in
	// rather than parse bogus "playlists".
	raw := []byte(`{
		"responseContext": {"serviceTrackingParams": [
			{"service": "GFEEDBACK", "params": [{"key": "logged_in", "value": "0"}]}
		]},
		"contents": {"gridRenderer": {"items": [
			{"musicTwoRowItemRenderer": {"title": {"runs": [{
				"text": "Some Promo",
				"navigationEndpoint": {"browseEndpoint": {"browseId": "VLPLwhatever"}}
			}]}}}
		]}}
	}`)
	_, err := LibraryPlaylists(raw)
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn (logged-out marker must win)", err)
	}
}

func TestLibraryPlaylists_emptyButSignedIn(t *testing.T) {
	// logged_in "1" with no grid is a signed-in empty library (e.g. an
	// empty-state message page), not ErrNotSignedIn.
	raw := []byte(`{
		"responseContext": {"serviceTrackingParams": [
			{"service": "GFEEDBACK", "params": [{"key": "logged_in", "value": "1"}]}
		]},
		"contents": {}
	}`)
	pls, err := LibraryPlaylists(raw)
	if err != nil {
		t.Fatalf("LibraryPlaylists on signed-in empty page: %v", err)
	}
	if len(pls) != 0 {
		t.Errorf("got %d playlists, want 0", len(pls))
	}
}

func TestLibraryPlaylists_skipsNonPlaylistTiles(t *testing.T) {
	// An album tile (MUSIC_PAGE_TYPE_ALBUM, MPREb id) in the grid must not
	// parse as a playlist.
	raw := []byte(`{"contents": {"gridRenderer": {"items": [
		{"musicTwoRowItemRenderer": {"title": {"runs": [{
			"text": "Some Album",
			"navigationEndpoint": {"browseEndpoint": {
				"browseId": "MPREb_abc123",
				"browseEndpointContextSupportedConfigs": {
					"browseEndpointContextMusicConfig": {"pageType": "MUSIC_PAGE_TYPE_ALBUM"}
				}
			}}
		}]}}},
		{"musicTwoRowItemRenderer": {"title": {"runs": [{
			"text": "Real Playlist",
			"navigationEndpoint": {"browseEndpoint": {
				"browseId": "VLPLreal",
				"browseEndpointContextSupportedConfigs": {
					"browseEndpointContextMusicConfig": {"pageType": "MUSIC_PAGE_TYPE_PLAYLIST"}
				}
			}}
		}]}}}
	]}}}`)
	pls, err := LibraryPlaylists(raw)
	if err != nil {
		t.Fatalf("LibraryPlaylists: %v", err)
	}
	if len(pls) != 1 || pls[0].ID != "PLreal" {
		t.Errorf("got %+v, want exactly the playlist tile (PLreal)", pls)
	}
}

func TestLibraryPlaylists_invalidJSON(t *testing.T) {
	if _, err := LibraryPlaylists([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestPlaylistTracks_fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/playlist_tracks.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	tracks, err := PlaylistTracks(data)
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}

	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 (malformed row skipped)", len(tracks))
	}

	t0 := tracks[0]
	if t0.VideoID != "fJ9rUzIMcZQ" {
		t.Errorf("track[0].VideoID = %q, want %q", t0.VideoID, "fJ9rUzIMcZQ")
	}
	if t0.Title != "Bohemian Rhapsody" {
		t.Errorf("track[0].Title = %q, want %q", t0.Title, "Bohemian Rhapsody")
	}
	if len(t0.Artists) != 1 || t0.Artists[0] != "Queen" {
		t.Errorf("track[0].Artists = %v, want [Queen]", t0.Artists)
	}
	if t0.Album != "A Night at the Opera" {
		t.Errorf("track[0].Album = %q, want %q", t0.Album, "A Night at the Opera")
	}
	if want := 5*time.Minute + 55*time.Second; t0.Duration != want {
		t.Errorf("track[0].Duration = %v, want %v", t0.Duration, want)
	}
	if t0.ThumbURL != "https://lh3.googleusercontent.com/large" {
		t.Errorf("track[0].ThumbURL = %q, want largest thumbnail", t0.ThumbURL)
	}

	t1 := tracks[1]
	if t1.VideoID != "hTWKbfoikeg" || t1.Title != "Smells Like Teen Spirit" {
		t.Errorf("track[1] = {%q, %q}, want {hTWKbfoikeg, Smells Like Teen Spirit}", t1.VideoID, t1.Title)
	}
}

func TestPlaylistTracks_notSignedIn(t *testing.T) {
	// No musicPlaylistShelfRenderer and no logged_in marker => logged-out page
	// => ErrNotSignedIn.
	_, err := PlaylistTracks([]byte(`{"contents":{}}`))
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
}

func TestPlaylistTracks_loggedOutFixture(t *testing.T) {
	// Trimmed live anonymous VLLM capture: logged_in "0" plus a sign-in
	// messageRenderer instead of the playlist shelf.
	data, err := os.ReadFile("testdata/playlist_tracks_logged_out.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, err = PlaylistTracks(data)
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
}

func TestPlaylistTracks_emptyButSignedInMarker(t *testing.T) {
	// logged_in "1" with no shelf at all is a signed-in empty playlist page
	// (e.g. zero liked songs rendered as an empty-state message), not
	// ErrNotSignedIn.
	raw := []byte(`{
		"responseContext": {"serviceTrackingParams": [
			{"service": "GFEEDBACK", "params": [{"key": "logged_in", "value": "1"}]}
		]},
		"contents": {}
	}`)
	tracks, err := PlaylistTracks(raw)
	if err != nil {
		t.Fatalf("PlaylistTracks on signed-in empty page: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestPlaylistTracks_anonymousPublicPlaylist(t *testing.T) {
	// A public playlist browses fine anonymously: logged_in "0" with a present
	// shelf must parse the tracks, not return ErrNotSignedIn.
	raw := []byte(`{
		"responseContext": {"serviceTrackingParams": [
			{"service": "GFEEDBACK", "params": [{"key": "logged_in", "value": "0"}]}
		]},
		"contents": {"musicPlaylistShelfRenderer": {"contents": [
			{"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "dQw4w9WgXcQ"},
				"flexColumns": [
					{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Public Song"}]}}}
				]
			}}
		]}}
	}`)
	tracks, err := PlaylistTracks(raw)
	if err != nil {
		t.Fatalf("PlaylistTracks on anonymous public playlist: %v", err)
	}
	if len(tracks) != 1 || tracks[0].VideoID != "dQw4w9WgXcQ" {
		t.Errorf("got %+v, want the one public track", tracks)
	}
}

func TestPlaylistTracks_emptyButSignedIn(t *testing.T) {
	// A present-but-empty shelf is a valid empty playlist, not ErrNotSignedIn.
	raw := []byte(`{"musicPlaylistShelfRenderer":{"contents":[]}}`)
	tracks, err := PlaylistTracks(raw)
	if err != nil {
		t.Fatalf("PlaylistTracks on empty shelf: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestPlaylistTracks_invalidJSON(t *testing.T) {
	if _, err := PlaylistTracks([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
