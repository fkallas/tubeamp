// Package model holds the shared domain types passed between packages.
package model

import (
	"strings"
	"time"
)

// Track is a playable YT Music song. AlbumID, when non-empty, is the MPRE…
// browseId of the album the song belongs to, threaded through from the parser so
// the UI can open the album from a song row; it is "" when the source row carried
// no resolvable album reference (the UI then treats the song as album-less).
type Track struct {
	VideoID  string
	Title    string
	Artists  []string
	Album    string
	AlbumID  string
	Duration time.Duration
	ThumbURL string
}

// ArtistLine renders the artist list for display.
func (t Track) ArtistLine() string {
	return strings.Join(t.Artists, ", ")
}

// URL is the canonical YT Music watch URL; mpv resolves it via yt-dlp.
func (t Track) URL() string {
	return "https://music.youtube.com/watch?v=" + t.VideoID
}

// Playlist is a user playlist (metadata only; tracks fetched separately).
type Playlist struct {
	ID         string
	Title      string
	TrackCount int
}

// Album groups tracks under a browseable YT Music album page.
type Album struct {
	BrowseID string
	Title    string
	Artists  []string
	Year     string
	ThumbURL string
}

// Artist is a browseable YT Music artist page.
type Artist struct {
	BrowseID string
	Name     string
	ThumbURL string
}
