package panels

import (
	"strings"

	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
)

// albumGlyph marks an album row in the search results.
const albumGlyph = "▤"

// SearchView renders search results as two sections — "Songs" (tracks) then
// "Albums" — under muted section headers. Selection is a single combined cursor:
// indices [0, len(tracks)) address songs and [len(tracks), len(tracks)+len(albums))
// address albums, so up/down moves through both sections seamlessly. Empty
// sections are omitted; with no results at all a placeholder line is shown.
func SearchView(th *theme.Theme, title string, tracks []model.Track, albums []model.Album, cursor int, playingVideoID string, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2

	if len(tracks) == 0 && len(albums) == 0 {
		return Box(th, title, []string{th.Muted().Render("  no results")}, w, h, focused)
	}

	rest := innerW - 4 // minus the cursor (2) and marker (2) columns.
	if rest < 0 {
		rest = 0
	}

	// entry is one display row: a section header (sel == -1) or a selectable item.
	type entry struct {
		header  bool
		label   string // plain text (header label, or rest-wide item label)
		styled  string // pre-styled item label (ignored for headers/selected rows)
		sel     int    // selection index, or -1 for headers
		playing bool
	}
	var entries []entry

	if len(tracks) > 0 {
		entries = append(entries, entry{header: true, label: "Songs", sel: -1})
		lay := trackLayout(tracks, rest, w >= albumMinMainWidth)
		for i, t := range tracks {
			plain, styled := lay.row(th, t)
			entries = append(entries, entry{
				label:   plain,
				styled:  styled,
				sel:     i,
				playing: playingVideoID != "" && t.VideoID == playingVideoID,
			})
		}
	}
	if len(albums) > 0 {
		entries = append(entries, entry{header: true, label: "Albums", sel: -1})
		for j, a := range albums {
			cell := PadPlain(Clip(albumLabel(a), rest), rest)
			entries = append(entries, entry{label: cell, styled: th.Primary().Render(cell), sel: len(tracks) + j})
		}
	}

	// Find the display row of the current selection so it stays visible.
	cursorPos := 0
	for i, e := range entries {
		if !e.header && e.sel == cursor {
			cursorPos = i
			break
		}
	}
	start, end := visibleWindow(cursorPos, len(entries), visible)

	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		e := entries[i]
		if e.header {
			body = append(body, th.Muted().Render(PadPlain(Clip(e.label, innerW), innerW)))
			continue
		}
		body = append(body, renderListRow(th, e.label, innerW, rowOpts{
			selected:    e.sel == cursor,
			focused:     focused,
			playing:     e.playing,
			showMarker:  true,
			styledField: e.styled,
		}))
	}
	return Box(th, title, body, w, h, focused)
}

// albumLabel formats an album search row as "▤ Title — Artists (Year)", omitting
// the artist and year segments when absent.
func albumLabel(a model.Album) string {
	s := albumGlyph + " " + a.Title
	if al := strings.Join(a.Artists, ", "); al != "" {
		s += " — " + al
	}
	if a.Year != "" {
		s += " (" + a.Year + ")"
	}
	return s
}
