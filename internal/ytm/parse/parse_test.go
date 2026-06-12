package parse

import (
	"os"
	"testing"
	"time"
)

func TestSearchTracks_fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/search_songs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	tracks, err := SearchTracks(data)
	if err != nil {
		t.Fatalf("SearchTracks: %v", err)
	}

	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 (malformed item must be skipped)", len(tracks))
	}

	// --- Track 0: Never Gonna Give You Up ---
	t0 := tracks[0]
	if t0.VideoID != "dQw4w9WgXcQ" {
		t.Errorf("track[0].VideoID = %q, want %q", t0.VideoID, "dQw4w9WgXcQ")
	}
	if t0.Title != "Never Gonna Give You Up" {
		t.Errorf("track[0].Title = %q, want %q", t0.Title, "Never Gonna Give You Up")
	}
	if len(t0.Artists) != 1 || t0.Artists[0] != "Rick Astley" {
		t.Errorf("track[0].Artists = %v, want [Rick Astley]", t0.Artists)
	}
	if t0.Album != "Whenever You Need Somebody" {
		t.Errorf("track[0].Album = %q, want %q", t0.Album, "Whenever You Need Somebody")
	}
	wantDur0 := 3*time.Minute + 32*time.Second
	if t0.Duration != wantDur0 {
		t.Errorf("track[0].Duration = %v, want %v", t0.Duration, wantDur0)
	}
	if t0.ThumbURL != "https://lh3.googleusercontent.com/large" {
		t.Errorf("track[0].ThumbURL = %q, want larger thumbnail", t0.ThumbURL)
	}

	// --- Track 1: Bohemian Rhapsody ---
	t1 := tracks[1]
	if t1.VideoID != "fJ9rUzIMcZQ" {
		t.Errorf("track[1].VideoID = %q, want %q", t1.VideoID, "fJ9rUzIMcZQ")
	}
	if t1.Title != "Bohemian Rhapsody" {
		t.Errorf("track[1].Title = %q, want %q", t1.Title, "Bohemian Rhapsody")
	}
	if len(t1.Artists) != 1 || t1.Artists[0] != "Queen" {
		t.Errorf("track[1].Artists = %v, want [Queen]", t1.Artists)
	}
	if t1.Album != "A Night at the Opera" {
		t.Errorf("track[1].Album = %q, want %q", t1.Album, "A Night at the Opera")
	}
	wantDur1 := 5*time.Minute + 55*time.Second
	if t1.Duration != wantDur1 {
		t.Errorf("track[1].Duration = %v, want %v", t1.Duration, wantDur1)
	}
	if t1.ThumbURL != "https://lh3.googleusercontent.com/bohemian_large" {
		t.Errorf("track[1].ThumbURL = %q, want larger thumbnail", t1.ThumbURL)
	}
}

func TestSearchTracks_empty(t *testing.T) {
	tracks, err := SearchTracks([]byte(`{}`))
	if err != nil {
		t.Fatalf("SearchTracks on empty object: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestSearchTracks_invalidJSON(t *testing.T) {
	_, err := SearchTracks([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
		ok    bool
	}{
		{"3:47", 3*time.Minute + 47*time.Second, true},
		{"1:02:33", time.Hour + 2*time.Minute + 33*time.Second, true},
		{"0:00", 0, true},
		{"", 0, false},
		{"abc", 0, false},
		{"1:2:3:4", 0, false},
	}
	for _, tc := range tests {
		d, ok := parseDuration(tc.input)
		if ok != tc.ok {
			t.Errorf("parseDuration(%q) ok=%v, want %v", tc.input, ok, tc.ok)
			continue
		}
		if ok && d != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.input, d, tc.want)
		}
	}
}
