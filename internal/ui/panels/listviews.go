package panels

import (
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// ArtistListView renders the Library "Artists" main view: one selectable row per
// pre-formatted artist label (e.g. "♪ Queen (12)"). It mirrors the plain track
// table's row chrome (a "> " cursor on the selection) so the panel feels of a
// piece with the rest of the main view.
func ArtistListView(th *theme.Theme, title string, labels []string, cursor, w, h int, focused bool) string {
	return simpleListView(th, title, labels, cursor, w, h, focused)
}

// AlbumListView renders the Library "Albums" main view: one selectable row per
// album, formatted like the search-results album rows ("▤ Title — Artists
// (Year)"). enter/'o' on a row reuse the existing GetAlbum flow.
func AlbumListView(th *theme.Theme, title string, albums []model.Album, cursor, w, h int, focused bool) string {
	labels := make([]string, len(albums))
	for i, a := range albums {
		labels[i] = albumLabel(a)
	}
	return simpleListView(th, title, labels, cursor, w, h, focused)
}

// simpleListView renders a flat list of pre-formatted labels into a bordered box
// with a selection cursor, shared by the artist and album list views.
func simpleListView(th *theme.Theme, title string, labels []string, cursor, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2

	if len(labels) == 0 {
		return Box(th, title, []string{th.Muted().Render("  nothing here")}, w, h, focused)
	}

	rest := innerW - 4 // minus the cursor (2) and marker (2) columns.
	if rest < 0 {
		rest = 0
	}

	start, end := visibleWindow(cursor, len(labels), visible)
	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		cell := PadPlain(Clip(labels[i], rest), rest)
		body = append(body, renderListRow(th, cell, innerW, rowOpts{
			selected:    i == cursor,
			focused:     focused,
			showMarker:  true,
			styledField: th.Primary().Render(cell),
		}))
	}
	return Box(th, title, body, w, h, focused)
}
