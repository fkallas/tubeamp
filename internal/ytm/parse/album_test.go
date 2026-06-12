package parse

import (
	"os"
	"testing"
	"time"
)

func TestSearchAlbums_fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/search_albums.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	albums, err := SearchAlbums(data)
	if err != nil {
		t.Fatalf("SearchAlbums: %v", err)
	}

	if len(albums) != 3 {
		t.Fatalf("got %d albums, want 3 (malformed item must be skipped)", len(albums))
	}

	// --- Album 0: OK COMPUTER. (SIDE B) ---
	a0 := albums[0]
	if a0.BrowseID != "MPREb_lPqSSFCbf4S" {
		t.Errorf("album[0].BrowseID = %q, want %q", a0.BrowseID, "MPREb_lPqSSFCbf4S")
	}
	if a0.Title != "OK COMPUTER. (SIDE B)" {
		t.Errorf("album[0].Title = %q", a0.Title)
	}
	if len(a0.Artists) != 1 || a0.Artists[0] != "MONTROPOLIS" {
		t.Errorf("album[0].Artists = %v, want [MONTROPOLIS]", a0.Artists)
	}
	if a0.Year != "2021" {
		t.Errorf("album[0].Year = %q, want 2021", a0.Year)
	}
	wantThumb := "https://yt3.googleusercontent.com/wQciCa58U8mCR---1xlpZ8QR35i_j8BNXchKX9cNbE7IT19TWsouSLzfEQFFv8znufrremvYOtlE9FaT=w544-h544-l90-rj"
	if a0.ThumbURL != wantThumb {
		t.Errorf("album[0].ThumbURL = %q, want largest", a0.ThumbURL)
	}

	// --- Album 1: Her Loss (two artists) ---
	a1 := albums[1]
	if a1.BrowseID != "MPREb_7hM2FHsf84t" {
		t.Errorf("album[1].BrowseID = %q", a1.BrowseID)
	}
	if len(a1.Artists) != 2 || a1.Artists[0] != "Drake" || a1.Artists[1] != "21 Savage" {
		t.Errorf("album[1].Artists = %v, want [Drake 21 Savage]", a1.Artists)
	}
	if a1.Year != "2022" {
		t.Errorf("album[1].Year = %q, want 2022", a1.Year)
	}

	// --- Album 2: Purple Rain ---
	a2 := albums[2]
	if a2.Title != "Purple Rain (Deluxe Expanded Edition)" {
		t.Errorf("album[2].Title = %q", a2.Title)
	}
	if len(a2.Artists) != 1 || a2.Artists[0] != "Prince" {
		t.Errorf("album[2].Artists = %v, want [Prince]", a2.Artists)
	}
	if a2.Year != "1984" {
		t.Errorf("album[2].Year = %q, want 1984", a2.Year)
	}
}

func TestSearchAlbums_cardShelf(t *testing.T) {
	// Top-result musicCardShelfRenderer pointing at an album. Handcrafted from
	// ytmusicapi's documented shape (no live capture filtered to a card shelf).
	raw := []byte(`{
	  "contents": {"tabbedSearchResultsRenderer": {"tabs": [{"tabRenderer": {"content": {
	    "sectionListRenderer": {"contents": [
	      {"musicCardShelfRenderer": {
	        "title": {"runs": [{"text": "OK Computer", "navigationEndpoint": {"browseEndpoint": {
	          "browseId": "MPREb_cardTopResult",
	          "browseEndpointContextSupportedConfigs": {"browseEndpointContextMusicConfig": {"pageType": "MUSIC_PAGE_TYPE_ALBUM"}}}}}]},
	        "subtitle": {"runs": [
	          {"text": "Album"}, {"text": " • "},
	          {"text": "Radiohead", "navigationEndpoint": {"browseEndpoint": {"browseId": "UCradiohead",
	            "browseEndpointContextSupportedConfigs": {"browseEndpointContextMusicConfig": {"pageType": "MUSIC_PAGE_TYPE_ARTIST"}}}}},
	          {"text": " • "}, {"text": "1997"}]},
	        "thumbnail": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [
	          {"url": "https://lh3.googleusercontent.com/okc_small", "width": 60, "height": 60},
	          {"url": "https://lh3.googleusercontent.com/okc_large", "width": 544, "height": 544}]}}}}}
	    ]}}}}]}}
	}`)

	albums, err := SearchAlbums(raw)
	if err != nil {
		t.Fatalf("SearchAlbums: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("got %d albums, want 1", len(albums))
	}
	a := albums[0]
	if a.BrowseID != "MPREb_cardTopResult" {
		t.Errorf("BrowseID = %q", a.BrowseID)
	}
	if a.Title != "OK Computer" {
		t.Errorf("Title = %q", a.Title)
	}
	if len(a.Artists) != 1 || a.Artists[0] != "Radiohead" {
		t.Errorf("Artists = %v, want [Radiohead]", a.Artists)
	}
	if a.Year != "1997" {
		t.Errorf("Year = %q, want 1997", a.Year)
	}
	if a.ThumbURL != "https://lh3.googleusercontent.com/okc_large" {
		t.Errorf("ThumbURL = %q, want largest", a.ThumbURL)
	}
}

func TestSearchAlbums_empty(t *testing.T) {
	albums, err := SearchAlbums([]byte(`{}`))
	if err != nil {
		t.Fatalf("SearchAlbums on empty object: %v", err)
	}
	if len(albums) != 0 {
		t.Errorf("got %d albums, want 0", len(albums))
	}
}

func TestSearchAlbums_invalidJSON(t *testing.T) {
	if _, err := SearchAlbums([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestAlbumPage_responsiveHeader(t *testing.T) {
	data, err := os.ReadFile("testdata/album_page.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	album, tracks, err := AlbumPage(data)
	if err != nil {
		t.Fatalf("AlbumPage: %v", err)
	}

	if album.Title != "OK COMPUTER. (SIDE B)" {
		t.Errorf("album.Title = %q", album.Title)
	}
	if len(album.Artists) != 1 || album.Artists[0] != "MONTROPOLIS" {
		t.Errorf("album.Artists = %v, want [MONTROPOLIS]", album.Artists)
	}
	if album.Year != "2021" {
		t.Errorf("album.Year = %q, want 2021", album.Year)
	}
	wantThumb := "https://yt3.googleusercontent.com/wQciCa58U8mCR---1xlpZ8QR35i_j8BNXchKX9cNbE7IT19TWsouSLzfEQFFv8znufrremvYOtlE9FaT=w544-h544-l90-rj"
	if album.ThumbURL != wantThumb {
		t.Errorf("album.ThumbURL = %q, want largest", album.ThumbURL)
	}

	if len(tracks) != 3 {
		t.Fatalf("got %d tracks, want 3 (malformed track must be skipped)", len(tracks))
	}

	t0 := tracks[0]
	if t0.VideoID != "1hst4Sk1QQU" {
		t.Errorf("track[0].VideoID = %q", t0.VideoID)
	}
	if t0.Title != "WELCOME TO THE CYBERSPACE" {
		t.Errorf("track[0].Title = %q", t0.Title)
	}
	// Per-track artist column is empty -> falls back to album artists.
	if len(t0.Artists) != 1 || t0.Artists[0] != "MONTROPOLIS" {
		t.Errorf("track[0].Artists = %v, want fallback [MONTROPOLIS]", t0.Artists)
	}
	if t0.Album != "OK COMPUTER. (SIDE B)" {
		t.Errorf("track[0].Album = %q, want album title", t0.Album)
	}
	if t0.ThumbURL != wantThumb {
		t.Errorf("track[0].ThumbURL = %q, want album thumb", t0.ThumbURL)
	}
	wantDur := 2*time.Minute + 58*time.Second
	if t0.Duration != wantDur {
		t.Errorf("track[0].Duration = %v, want %v", t0.Duration, wantDur)
	}

	if tracks[2].VideoID != "k0Kaqckv-Ro" || tracks[2].Title != "DIE HÖLLE (Remix)" {
		t.Errorf("track[2] = %q/%q", tracks[2].VideoID, tracks[2].Title)
	}
}

func TestAlbumPage_detailHeader(t *testing.T) {
	data, err := os.ReadFile("testdata/album_page_detail.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	album, tracks, err := AlbumPage(data)
	if err != nil {
		t.Fatalf("AlbumPage: %v", err)
	}

	if album.Title != "A Night at the Opera" {
		t.Errorf("album.Title = %q", album.Title)
	}
	if len(album.Artists) != 1 || album.Artists[0] != "Queen" {
		t.Errorf("album.Artists = %v, want [Queen]", album.Artists)
	}
	if album.Year != "1975" {
		t.Errorf("album.Year = %q, want 1975", album.Year)
	}
	// croppedSquareThumbnailRenderer nesting, largest wins.
	if album.ThumbURL != "https://lh3.googleusercontent.com/opera_large" {
		t.Errorf("album.ThumbURL = %q, want largest", album.ThumbURL)
	}

	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 (malformed skipped)", len(tracks))
	}

	// Track 0 has its own artist column.
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "Freddie Mercury" {
		t.Errorf("track[0].Artists = %v, want [Freddie Mercury]", tracks[0].Artists)
	}
	if tracks[0].Album != "A Night at the Opera" {
		t.Errorf("track[0].Album = %q", tracks[0].Album)
	}
	// Track 1 has no artist column -> falls back to album artists.
	if len(tracks[1].Artists) != 1 || tracks[1].Artists[0] != "Queen" {
		t.Errorf("track[1].Artists = %v, want fallback [Queen]", tracks[1].Artists)
	}
	wantDur := 3*time.Minute + 39*time.Second
	if tracks[1].Duration != wantDur {
		t.Errorf("track[1].Duration = %v, want %v", tracks[1].Duration, wantDur)
	}
}

func TestAlbumPage_empty(t *testing.T) {
	album, tracks, err := AlbumPage([]byte(`{}`))
	if err != nil {
		t.Fatalf("AlbumPage on empty object: %v", err)
	}
	if album.Title != "" || len(tracks) != 0 {
		t.Errorf("expected zero album and no tracks, got %+v / %d tracks", album, len(tracks))
	}
}

func TestAlbumPage_invalidJSON(t *testing.T) {
	if _, _, err := AlbumPage([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
