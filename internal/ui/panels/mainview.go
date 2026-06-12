package panels

import (
	"time"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// MainView renders the contextual main panel: its border title is the current
// context (e.g. "Liked Songs", a playlist name, or a search query). The track
// at playingVideoID (if non-empty) is marked with a music note.
func MainView(th *theme.Theme, title string, tracks []model.Track, cursor int, playingVideoID string, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2

	if len(tracks) == 0 {
		body := []string{th.Muted().Render("  nothing here")}
		return Box(th, title, body, w, h, focused)
	}

	// rest = inner width minus cursor (2) and marker (2).
	rest := innerW - 4
	if rest < 0 {
		rest = 0
	}

	start, end := visibleWindow(cursor, len(tracks), visible)
	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		t := tracks[i]
		left := t.Title
		if a := t.ArtistLine(); a != "" {
			left = t.Title + "  —  " + a
		}
		label := leftRight(left, fmtDuration(t.Duration), rest)
		body = append(body, renderListRow(th, label, innerW, rowOpts{
			selected:   i == cursor,
			focused:    focused,
			playing:    playingVideoID != "" && t.VideoID == playingVideoID,
			showMarker: true,
		}))
	}
	return Box(th, title, body, w, h, focused)
}

// fmtDuration renders a track duration as "m:ss".
func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return FormatSeconds(d.Seconds())
}
