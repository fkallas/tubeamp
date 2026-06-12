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
	// A logged-out page has no gridRenderer => ErrNotSignedIn (not empty success).
	_, err := LibraryPlaylists([]byte(`{"contents":{}}`))
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
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
	// No musicPlaylistShelfRenderer => logged-out page => ErrNotSignedIn.
	_, err := PlaylistTracks([]byte(`{"contents":{}}`))
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
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
