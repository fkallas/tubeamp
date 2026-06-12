package panels

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// queueAlbumMinWidth is the smallest queue panel width (outer columns) at which
// rows append a muted " — <album>" suffix; below it rows are left unchanged.
const queueAlbumMinWidth = 34

// Queue renders the "3 Queue" panel. cursor is the navigation cursor row;
// playingIdx is the index of the currently-playing track (-1 if none), which is
// marked with a music note. When the panel is wide enough each row appends a
// muted " — <album>" suffix. An empty queue shows a muted placeholder.
func Queue(th *theme.Theme, tracks []model.Track, cursor, playingIdx, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2

	if len(tracks) == 0 {
		body := []string{th.Muted().Render("  queue empty")}
		return Box(th, "3 Queue", body, w, h, focused)
	}

	rest := innerW - 4 // inner width minus cursor (2) and marker (2) columns.
	if rest < 0 {
		rest = 0
	}
	showAlbum := w >= queueAlbumMinWidth

	start, end := visibleWindow(cursor, len(tracks), visible)
	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		t := tracks[i]
		plain, styled := queueRow(th, t, rest, showAlbum)
		body = append(body, renderListRow(th, plain, innerW, rowOpts{
			selected:    i == cursor,
			focused:     focused,
			playing:     i == playingIdx,
			showMarker:  true,
			styledField: styled,
		}))
	}
	return Box(th, "3 Queue", body, w, h, focused)
}

// queueRow renders a queue row to its plain form and, when an album suffix is
// shown, a parallel styled form with that suffix in Muted. styled is empty for
// the unchanged (narrow / album-less) case so the caller renders solid Primary.
func queueRow(th *theme.Theme, t model.Track, rest int, showAlbum bool) (plain, styled string) {
	artist := t.ArtistLine()
	if !showAlbum || t.Album == "" {
		if artist != "" {
			return leftRight(t.Title, artist, rest), ""
		}
		return t.Title, ""
	}

	suffix := " — " + t.Album
	if maxW := rest / 2; lipgloss.Width(suffix) > maxW {
		suffix = Clip(suffix, maxW)
	}
	sw := lipgloss.Width(suffix)

	left := t.Title
	if artist != "" {
		left = leftRight(t.Title, artist, rest-sw)
	}
	left = PadPlain(left, rest-sw)
	plain = left + suffix
	styled = th.Primary().Render(left) + th.Muted().Render(suffix)
	return plain, styled
}
