package panels

import (
	"fmt"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// Playlists renders the "2 Playlists" panel listing each playlist's title and
// track count, with the selection cursor on row cursor.
func Playlists(th *theme.Theme, pls []model.Playlist, cursor, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2
	start, end := visibleWindow(cursor, len(pls), visible)
	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		pl := pls[i]
		label := pl.Title
		if pl.TrackCount > 0 {
			label = leftRight(pl.Title, fmt.Sprintf("%d", pl.TrackCount), innerW-2)
		}
		body = append(body, renderListRow(th, label, innerW, rowOpts{
			selected: i == cursor,
			focused:  focused,
		}))
	}
	return Box(th, "2 Playlists", body, w, h, focused)
}
