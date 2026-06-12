package panels

import "github.com/fkallas/tubeamp/internal/theme"

// Library renders the "1 Library" panel: a fixed list of library sections with
// the selection cursor on row cursor. w×h are the outer box dimensions.
func Library(th *theme.Theme, items []string, cursor, w, h int, focused bool) string {
	innerW := w - 2
	visible := h - 2
	start, end := visibleWindow(cursor, len(items), visible)
	body := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		body = append(body, renderListRow(th, items[i], innerW, rowOpts{
			selected: i == cursor,
			focused:  focused,
		}))
	}
	return Box(th, "1 Library", body, w, h, focused)
}
