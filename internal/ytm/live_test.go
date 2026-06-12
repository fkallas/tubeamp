package ytm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/config"
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

// TestAccountInfo_liveAuth performs a real authenticated account/account_menu
// call using the on-disk auth file. Gated behind TUBEAMP_LIVE_AUTH=1 because the
// cookie is the user's real credential; skipped by default. It logs only the
// resolved sign-in state and account name — never the cookie.
func TestAccountInfo_liveAuth(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE_AUTH") != "1" {
		t.Skip("set TUBEAMP_LIVE_AUTH=1 to run authenticated live tests")
	}

	auth, err := LoadAuth(filepath.Join(config.DataDir(), "auth"))
	if err != nil {
		t.Skipf("no auth file available: %v", err)
	}
	c := NewClient(auth)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	name, signedIn, err := c.AccountInfo(ctx)
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	t.Logf("live AccountInfo: signedIn=%v name=%q", signedIn, name)
}

// TestAccountInfo_liveAnonymous validates the logged-out account_menu shape
// against the real endpoint with NO credentials: an anonymous client must
// resolve as ("", false, nil), matching the account_logged_out.json fixture's
// assumption. Skipped unless TUBEAMP_LIVE=1.
func TestAccountInfo_liveAnonymous(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE") != "1" {
		t.Skip("set TUBEAMP_LIVE=1 to run live network tests")
	}

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	name, signedIn, err := c.AccountInfo(ctx)
	if err != nil {
		t.Fatalf("AccountInfo (anonymous): %v", err)
	}
	if signedIn || name != "" {
		t.Errorf("anonymous AccountInfo = (%q, %v), want (\"\", false)", name, signedIn)
	}
}

// TestLibrary_liveAnonymous validates the logged-out library discriminator
// against the real endpoints with NO credentials: anonymous LibraryPlaylists
// and LikedSongs browses must surface ErrNotSignedIn — never an empty success
// or bogus data. Skipped unless TUBEAMP_LIVE=1.
func TestLibrary_liveAnonymous(t *testing.T) {
	if os.Getenv("TUBEAMP_LIVE") != "1" {
		t.Skip("set TUBEAMP_LIVE=1 to run live network tests")
	}

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := c.LibraryPlaylists(ctx); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("anonymous LibraryPlaylists err = %v, want ErrNotSignedIn", err)
	}
	if _, err := c.LikedSongs(ctx); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("anonymous LikedSongs err = %v, want ErrNotSignedIn", err)
	}
}
