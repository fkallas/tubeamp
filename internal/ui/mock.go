package ui

import (
	"time"

	"github.com/fkallas/tubeamp/internal/model"
)

// thumbURL builds the mqdefault thumbnail URL for a YouTube video id.
func thumbURL(videoID string) string {
	return "https://i.ytimg.com/vi/" + videoID + "/mqdefault.jpg"
}

// mt constructs a mock Track. dur is a "MmSs"-style duration in seconds passed
// as time.Duration by the caller.
func mt(videoID, title string, artists []string, album string, dur time.Duration) model.Track {
	return model.Track{
		VideoID:  videoID,
		Title:    title,
		Artists:  artists,
		Album:    album,
		Duration: dur,
		ThumbURL: thumbURL(videoID),
	}
}

// mockTracks holds the eight demo tracks (real video ids so playback works).
var mockTracks = []model.Track{
	mt("dQw4w9WgXcQ", "Never Gonna Give You Up", []string{"Rick Astley"}, "Whenever You Need Somebody", 3*time.Minute+33*time.Second),
	mt("9bZkp7q19f0", "Gangnam Style", []string{"PSY"}, "Psy 6 (Six Rules), Part 1", 4*time.Minute+13*time.Second),
	mt("kJQP7kiw5Fk", "Despacito", []string{"Luis Fonsi", "Daddy Yankee"}, "VIDA", 4*time.Minute+42*time.Second),
	mt("fJ9rUzIMcZQ", "Bohemian Rhapsody", []string{"Queen"}, "A Night at the Opera", 5*time.Minute+59*time.Second),
	mt("JGwWNGJdvx8", "Shape of You", []string{"Ed Sheeran"}, "÷ (Divide)", 4*time.Minute+24*time.Second),
	mt("OPf0YbXqDm0", "Uptown Funk", []string{"Mark Ronson", "Bruno Mars"}, "Uptown Special", 4*time.Minute+31*time.Second),
	mt("hTWKbfoikeg", "Smells Like Teen Spirit", []string{"Nirvana"}, "Nevermind", 5*time.Minute+2*time.Second),
	mt("60ItHLz5WEA", "Faded", []string{"Alan Walker"}, "Different World", 3*time.Minute+33*time.Second),
}

// pick returns the subset of mockTracks at the given 1-based indices.
func pick(idx ...int) []model.Track {
	out := make([]model.Track, 0, len(idx))
	for _, i := range idx {
		if i >= 1 && i <= len(mockTracks) {
			out = append(out, mockTracks[i-1])
		}
	}
	return out
}

// libraryItems lists the library sections shown in the "1 Library" panel.
func libraryItems() []string {
	return []string{"Liked Songs", "Albums", "Artists", "Songs", "History"}
}

// mockLibraryTracks returns the tracks for a library section. Unimplemented
// sections fall back to the full list; History shows a recent subset.
func mockLibraryTracks(name string) []model.Track {
	switch name {
	case "History":
		return pick(6, 7, 8)
	default: // Liked Songs, Albums, Artists, Songs
		return pick(1, 2, 3, 4, 5, 6, 7, 8)
	}
}

// mockPlaylists lists the demo playlists for the "2 Playlists" panel.
func mockPlaylists() []model.Playlist {
	return []model.Playlist{
		{ID: "focus", Title: "Focus Deep Work", TrackCount: 2},
		{ID: "gym", Title: "Gym 2026", TrackCount: 3},
		{ID: "br", Title: "BR Indie", TrackCount: 2},
		{ID: "road", Title: "Road Trip", TrackCount: 3},
	}
}

// mockPlaylistTracks returns the tracks belonging to a playlist id.
func mockPlaylistTracks(id string) []model.Track {
	switch id {
	case "focus":
		return pick(4, 7)
	case "gym":
		return pick(2, 6, 8)
	case "br":
		return pick(1, 3)
	case "road":
		return pick(5, 6, 1)
	default:
		return nil
	}
}
