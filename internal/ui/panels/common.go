// Package panels renders tubeamp's bordered panels (library, playlists, queue,
// the main track list, and the player bar) in a Lazygit-inspired style: titles
// embedded in the top border, a "> " selection cursor, and a music-note marker
// on the playing track. All colours come from the active theme; nothing here
// hardcodes a colour.
package panels

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/theme"
)

// Rounded box-drawing runes used for panel borders.
const (
	cTL = "╭"
	cTR = "╮"
	cBL = "╰"
	cBR = "╯"
	cHZ = "─"
	cVT = "│"
)

// noteGlyph marks the currently-playing track wherever it is listed.
const noteGlyph = "♪"

// Clip shortens s to at most w display columns, appending an ellipsis when it
// truncates. It operates on plain (unstyled) text — do not pass ANSI-styled
// strings.
func Clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > w-1 {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	b.WriteString("…")
	return b.String()
}

// PadPlain pads s with trailing spaces to exactly w display columns, or clips
// it if it is wider. lipgloss.Width is used so ANSI-styled strings are measured
// correctly (padding only — clipping a styled string falls back to Clip on the
// raw text and may drop styling).
func PadPlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	cur := lipgloss.Width(s)
	if cur == w {
		return s
	}
	if cur < w {
		return s + strings.Repeat(" ", w-cur)
	}
	return Clip(s, w)
}

// Box renders body inside a rounded border of exactly w×h cells, with title
// embedded in the top border like Lazygit. When active the border uses the
// theme's active border colour and the title its active (accent) colour.
func Box(th *theme.Theme, title string, body []string, w, h int, active bool) string {
	if w < 2 || h < 2 {
		return ""
	}
	borderCol := th.Colors.BorderInactive
	if active {
		borderCol = th.Colors.BorderActive
	}
	bs := lipgloss.NewStyle().Foreground(lipgloss.Color(borderCol))
	innerW := w - 2

	var top string
	if title != "" {
		title = Clip(title, innerW-1)
		tstyled := th.Title(active).Render(title)
		tw := lipgloss.Width(tstyled)
		fill := innerW - 1 - tw
		if fill < 0 {
			fill = 0
		}
		top = bs.Render(cTL+cHZ) + tstyled + bs.Render(strings.Repeat(cHZ, fill)+cTR)
	} else {
		top = bs.Render(cTL + strings.Repeat(cHZ, innerW) + cTR)
	}

	rows := make([]string, 0, h)
	rows = append(rows, top)
	for i := 0; i < h-2; i++ {
		var content string
		if i < len(body) {
			content = body[i]
		}
		rows = append(rows, bs.Render(cVT)+PadPlain(content, innerW)+bs.Render(cVT))
	}
	rows = append(rows, bs.Render(cBL+strings.Repeat(cHZ, innerW)+cBR))
	return strings.Join(rows, "\n")
}

// rowOpts controls how a single list row is rendered.
type rowOpts struct {
	selected   bool // this row is under the selection cursor
	focused    bool // the owning panel currently has focus
	playing    bool // this row is the playing track (show the note marker)
	showMarker bool // reserve a marker column (queue + main view)
}

// renderListRow renders one list row to exactly innerW columns: a "> " cursor
// on the selected row (Selected style when focused, Muted when not), an
// optional music-note marker in Playing style on the playing track, then the
// label.
func renderListRow(th *theme.Theme, label string, innerW int, o rowOpts) string {
	markerW := 0
	if o.showMarker {
		markerW = 2
	}
	rest := innerW - 2 - markerW
	if rest < 0 {
		rest = 0
	}
	field := PadPlain(Clip(label, rest), rest)

	cursor := "  "
	if o.selected {
		cursor = "> "
	}

	if o.selected {
		st := th.Selected()
		if !o.focused {
			st = th.Muted()
		}
		if o.showMarker && o.playing {
			return st.Render(cursor) + th.PlayingStyle().Render(noteGlyph+" ") + st.Render(field)
		}
		marker := ""
		if o.showMarker {
			marker = "  "
		}
		return st.Render(cursor + marker + field)
	}

	if o.showMarker && o.playing {
		return cursor + th.PlayingStyle().Render(noteGlyph+" ") + th.Primary().Render(field)
	}
	marker := ""
	if o.showMarker {
		marker = "  "
	}
	return cursor + marker + th.Primary().Render(field)
}

// visibleWindow returns the [start, end) slice bounds of a list of total items
// that keeps cursor visible within visible rows.
func visibleWindow(cursor, total, visible int) (int, int) {
	if visible <= 0 || total == 0 {
		return 0, 0
	}
	if total <= visible {
		return 0, total
	}
	start := cursor - visible/2
	if start < 0 {
		start = 0
	}
	if start+visible > total {
		start = total - visible
	}
	return start, start + visible
}

// leftRight composes left and right onto a single line of width w, with right
// flushed to the far edge. The left (primary) content is prioritised: if right
// cannot fit alongside at least a clipped left plus a gap, only left is shown.
func leftRight(left, right string, w int) string {
	if w <= 0 {
		return ""
	}
	rw := lipgloss.Width(right)
	if rw == 0 || rw+2 > w {
		return Clip(left, w)
	}
	left = Clip(left, w-rw-1)
	lw := lipgloss.Width(left)
	gap := w - lw - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}
