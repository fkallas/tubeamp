package panels

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/lyrics"
	"github.com/fkallas/tubeamp/internal/theme"
)

// LyricsState selects what the lyrics panel displays.
type LyricsState int

const (
	LyricsLoading  LyricsState = iota // a lyrics lookup is still in flight
	LyricsNone                        // resolved, nothing found
	LyricsSynced                      // timed LRC lines, highlighted by playback position
	LyricsUnsynced                    // plain text, no highlight
)

// LyricsView renders the lyrics panel: a rounded bordered box titled "Lyrics"
// with a right-aligned state marker embedded in its top border. When active
// (the panel is the focused area) it uses the theme's active border + title
// colours like any focused panel; otherwise it stays inactive.
//
// Synced, following: a window of lines centered on current (the index
// lyrics.CurrentLine returns for the playback position), the current line in
// PlayingStyle, its immediate neighbours in Primary and the rest Muted, so the
// lyrics auto-scroll as playback advances. Synced, detached (a focused peek-
// scroll): the window centers on scroll (the peeked line index) instead and the
// marker shows the paused state. Unsynced: plain text from line scroll downward
// in Primary. Loading/None: a single Muted notice. Every row is exactly w
// columns wide.
func LyricsView(th *theme.Theme, st LyricsState, lines []lyrics.Line, plain string, current, scroll int, detached, active bool, w, h int) string {
	if w < 2 || h < 2 {
		return ""
	}
	borderCol := th.Colors.BorderInactive
	if active {
		borderCol = th.Colors.BorderActive
	}
	bs := lipgloss.NewStyle().Foreground(lipgloss.Color(borderCol))
	innerW := w - 2
	visible := h - 2

	content := lyricsContent(th, st, lines, plain, current, scroll, detached, innerW, visible)

	rows := make([]string, 0, h)
	rows = append(rows, lyricsTop(th, bs, st, detached, active, w))
	for i := 0; i < visible; i++ {
		var line string
		if i < len(content) {
			line = content[i]
		}
		rows = append(rows, bs.Render(cVT)+PadPlain(line, innerW)+bs.Render(cVT))
	}
	rows = append(rows, bs.Render(cBL+strings.Repeat(cHZ, innerW)+cBR))
	return strings.Join(rows, "\n")
}

// lyricsTop builds the top border with the "Lyrics" title at the left and, when
// it fits, the state marker right-aligned just inside the closing corner. The
// title uses the active style when the panel is focused. The total width is
// always exactly w columns.
func lyricsTop(th *theme.Theme, bs lipgloss.Style, st LyricsState, detached, active bool, w int) string {
	innerW := w - 2
	title := th.Title(active).Render(Clip("Lyrics", innerW-1))
	tw := lipgloss.Width(title)

	if marker := lyricsMarker(th, st, detached); marker != "" {
		mw := lipgloss.Width(marker)
		if fill := w - 4 - tw - mw; fill >= 1 {
			return bs.Render(cTL+cHZ) + title + bs.Render(strings.Repeat(cHZ, fill)) +
				marker + bs.Render(cHZ+cTR)
		}
	}
	// Loading (no marker) or the marker would not fit: plain top border.
	fill := innerW - 1 - tw
	if fill < 0 {
		fill = 0
	}
	return bs.Render(cTL+cHZ) + title + bs.Render(strings.Repeat(cHZ, fill)+cTR)
}

// lyricsMarker returns the right-aligned state marker for the top border, or ""
// while loading (no marker is shown until a state resolves). A synced panel that
// has been detached by a focused peek-scroll shows the paused-follow marker.
func lyricsMarker(th *theme.Theme, st LyricsState, detached bool) string {
	switch st {
	case LyricsSynced:
		if detached {
			return th.Muted().Render("⏸ paused — esc to follow")
		}
		return th.AccentStyle().Render(noteGlyph + " synced")
	case LyricsUnsynced:
		return th.Muted().Render("unsynced")
	case LyricsNone:
		return th.Muted().Render("no lyrics")
	default:
		return ""
	}
}

// lyricsContent builds the panel's content rows for the given state. For synced
// lyrics the window centers on scroll (the peeked line) when detached, else on
// current (the live line); for unsynced it renders from line scroll downward.
func lyricsContent(th *theme.Theme, st LyricsState, lines []lyrics.Line, plain string, current, scroll int, detached bool, innerW, visible int) []string {
	switch st {
	case LyricsNone:
		return []string{th.Muted().Render(Clip("  No lyrics found.", innerW))}
	case LyricsUnsynced:
		return plainLyricsLines(th, plain, scroll, innerW, visible)
	case LyricsSynced:
		center := current
		if detached {
			center = scroll
		}
		return syncedLyricsLines(th, lines, center, innerW, visible)
	default: // LyricsLoading
		return []string{th.Muted().Render(Clip("  searching for lyrics…", innerW))}
	}
}

// syncedLyricsLines renders a vertically-centered window of timed lines, the
// current line glowing in PlayingStyle, its neighbours in Primary and the rest
// Muted. Each line is horizontally centered to exactly innerW columns.
func syncedLyricsLines(th *theme.Theme, lines []lyrics.Line, current, innerW, visible int) []string {
	if len(lines) == 0 {
		return []string{th.Muted().Render(Clip("  No lyrics found.", innerW))}
	}
	start, end := visibleWindow(max(current, 0), len(lines), visible)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		text := lines[i].Text
		var style lipgloss.Style
		switch {
		case i == current:
			style = th.PlayingStyle()
			if text == "" {
				text = noteGlyph // a blank cue (instrumental) still shows the playhead
			}
		case i == current-1 || i == current+1:
			style = th.Primary()
		default:
			style = th.Muted()
		}
		clipped := Clip(text, innerW)
		out = append(out, centerLine(style.Render(clipped), lipgloss.Width(clipped), innerW))
	}
	return out
}

// plainLyricsLines renders unsynced text from line scroll downward, left-aligned
// in Primary. scroll is the top-line offset (0 = from the top); it is clamped so
// the panel never renders past the end of the text.
func plainLyricsLines(th *theme.Theme, plain string, scroll, innerW, visible int) []string {
	if strings.TrimSpace(plain) == "" {
		return []string{th.Muted().Render(Clip("  No lyrics found.", innerW))}
	}
	src := strings.Split(plain, "\n")
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(src) {
		scroll = len(src)
	}
	out := make([]string, 0, visible)
	for i := scroll; i < len(src) && len(out) < visible; i++ {
		out = append(out, th.Primary().Render(Clip(src[i], innerW)))
	}
	return out
}

// centerLine pads a styled string (whose visible width is vis) with spaces so it
// occupies exactly w columns, centered.
func centerLine(styled string, vis, w int) string {
	if vis >= w {
		return styled
	}
	left := (w - vis) / 2
	return strings.Repeat(" ", left) + styled + strings.Repeat(" ", w-vis-left)
}
