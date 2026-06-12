package panels

import (
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// Queue renders the "3 Queue" panel. cursor is the navigation cursor row;
// playingIdx is the index of the currently-playing track (-1 if none), which is
// marked with a music note. An empty queue shows a muted placeholder.
func Queue(th *theme.Theme, tracks []model.Track, cursor, playingIdx, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2

	if len(tracks) == 0 {
		body := []string{th.Muted().Render("  queue empty")}
		return Box(th, "3 Queue", body, w, h, focused)
	}

	start, end := visibleWindow(cursor, len(tracks), visible)
	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		t := tracks[i]
		label := t.Title
		if a := t.ArtistLine(); a != "" {
			label = leftRight(t.Title, a, innerW-4)
		}
		body = append(body, renderListRow(th, label, innerW, rowOpts{
			selected:   i == cursor,
			focused:    focused,
			playing:    i == playingIdx,
			showMarker: true,
		}))
	}
	return Box(th, "3 Queue", body, w, h, focused)
}
