package parse

import (
	"os"
	"testing"
	"time"
)

func TestTrackDetails_fixtureNowPlaying(t *testing.T) {
	data, err := os.ReadFile("testdata/trackdetails_next.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// No videoID => the now-playing (first) queue item.
	album, albumID, dur, err := TrackDetails(data, "")
	if err != nil {
		t.Fatalf("TrackDetails: %v", err)
	}
	if album != "A Night at the Opera" {
		t.Errorf("album = %q, want A Night at the Opera", album)
	}
	if albumID != "MPREb_nightAtTheOpera" {
		t.Errorf("albumID = %q, want MPREb_nightAtTheOpera", albumID)
	}
	if dur != 5*time.Minute+55*time.Second {
		t.Errorf("dur = %v, want 5:55", dur)
	}
}

func TestTrackDetails_matchByVideoID(t *testing.T) {
	data, err := os.ReadFile("testdata/trackdetails_next.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// The second queue item carries no album run on its byline — only the menu
	// "Go to album" id (name then stays ""). Exercises the menu fallback.
	album, albumID, dur, err := TrackDetails(data, "HaZpZQG2z10")
	if err != nil {
		t.Fatalf("TrackDetails: %v", err)
	}
	if album != "" {
		t.Errorf("album = %q, want empty (only menu id present)", album)
	}
	if albumID != "MPREb_aDayAtTheRaces" {
		t.Errorf("albumID = %q, want MPREb_aDayAtTheRaces (from menu)", albumID)
	}
	if dur != 2*time.Minute+52*time.Second {
		t.Errorf("dur = %v, want 2:52", dur)
	}
}

func TestTrackDetails_lengthSecondsFallback(t *testing.T) {
	raw := []byte(`{"playlistPanelVideoRenderer":{"videoId":"x","lengthSeconds":"203"}}`)
	_, _, dur, err := TrackDetails(raw, "x")
	if err != nil {
		t.Fatalf("TrackDetails: %v", err)
	}
	if dur != 203*time.Second {
		t.Errorf("dur = %v, want 203s", dur)
	}
}

func TestTrackDetails_noAlbumIsNotError(t *testing.T) {
	// A single with a duration but no album anywhere: valid zero-album result.
	raw := []byte(`{"playlistPanelVideoRenderer":{"videoId":"x","lengthText":{"runs":[{"text":"3:01"}]}}}`)
	album, albumID, dur, err := TrackDetails(raw, "x")
	if err != nil {
		t.Fatalf("TrackDetails: %v", err)
	}
	if album != "" || albumID != "" {
		t.Errorf("album/albumID = %q/%q, want empty", album, albumID)
	}
	if dur != 3*time.Minute+1*time.Second {
		t.Errorf("dur = %v, want 3:01", dur)
	}
}

func TestTrackDetails_noQueueItem(t *testing.T) {
	if _, _, _, err := TrackDetails([]byte(`{"contents":{}}`), ""); err == nil {
		t.Fatal("want error when no queue item is present")
	}
}

func TestTrackDetails_invalidJSON(t *testing.T) {
	if _, _, _, err := TrackDetails([]byte(`not json`), ""); err == nil {
		t.Fatal("want error for invalid JSON")
	}
}
