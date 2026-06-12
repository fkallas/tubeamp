package ytm

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSearch_live performs a real network call to YouTube Music (unauthenticated).
// Skipped unless TUBEAMP_LIVE=1.
func TestSearch_live(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE") != "1" {
		t.Skip("set TUBEAMP_LIVE=1 to run live network tests")
	}

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tracks, err := c.Search(ctx, "never gonna give you up")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tracks) == 0 {
		t.Fatal("expected >= 1 result, got 0")
	}
	t.Logf("live search returned %d tracks; first: %q by %v", len(tracks), tracks[0].Title, tracks[0].Artists)
}

// TestSearchAlbums_live performs real network calls: an albums-filtered search
// followed by a browse of the first result. Skipped unless TUBEAMP_LIVE=1.
func TestSearchAlbums_live(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE") != "1" {
		t.Skip("set TUBEAMP_LIVE=1 to run live network tests")
	}

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	albums, err := c.SearchAlbums(ctx, "ok computer")
	if err != nil {
		t.Fatalf("SearchAlbums: %v", err)
	}
	if len(albums) == 0 {
		t.Fatal("expected >= 1 album, got 0")
	}
	first := albums[0]
	t.Logf("live album search returned %d albums; first: %q (%s) by %v", len(albums), first.Title, first.BrowseID, first.Artists)

	if first.BrowseID == "" {
		t.Fatal("first album has empty BrowseID")
	}

	album, tracks, err := c.GetAlbum(ctx, first.BrowseID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.BrowseID != first.BrowseID {
		t.Errorf("GetAlbum BrowseID = %q, want %q", album.BrowseID, first.BrowseID)
	}
	if album.Title == "" {
		t.Error("GetAlbum returned empty album title")
	}
	if len(tracks) == 0 {
		t.Fatal("expected >= 1 track on album page, got 0")
	}
	t.Logf("live album page %q has %d tracks; first: %q (%s)", album.Title, len(tracks), tracks[0].Title, tracks[0].VideoID)
}
