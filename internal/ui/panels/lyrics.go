package panels

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fkallas/tubeamp/internal/lyrics"
	"github.com/fkallas/tubeamp/internal/theme"
)

// LyricsState selects what the (never-focusable) lyrics panel displays.
type LyricsState int

const (
	LyricsLoading  LyricsState = iota // a lyrics lookup is still in flight
	LyricsNone                        // resolved, nothing found
	LyricsSynced                      // timed LRC lines, highlighted by playback position
	LyricsUnsynced                    // plain text, no highlight
)

// LyricsView renders the synced-lyrics panel: a rounded, never-active bordered
// box titled "Lyrics" with a right-aligned state marker embedded in its top
// border. It is pure display — it handles no input and is never a focus target,
// so the border always uses the theme's inactive colour (PanelBorder(false)).
//
// Synced: a window of lines centered on current (the index lyrics.CurrentLine
// returns for the playback position), with the current line in PlayingStyle, its
// immediate neighbours in Primary and the rest Muted, so the lyrics auto-scroll
// as playback advances. Unsynced: plain text from the top in Primary.
// Loading/None: a single Muted notice. Every row is exactly w columns wide.
func LyricsView(th *theme.Theme, st LyricsState, lines []lyrics.Line, plain string, current, w, h int) string {
	if w < 2 || h < 2 {
		return ""
	}
	bs := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Colors.BorderInactive))
	innerW := w - 2
	visible := h - 2

	content := lyricsContent(th, st, lines, plain, current, innerW, visible)

	rows := make([]string, 0, h)
	rows = append(rows, lyricsTop(th, bs, st, w))
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
// total width is always exactly w columns.
func lyricsTop(th *theme.Theme, bs lipgloss.Style, st LyricsState, w int) string {
	innerW := w - 2
	title := th.Title(false).Render(Clip("Lyrics", innerW-1))
	tw := lipgloss.Width(title)

	if marker := lyricsMarker(th, st); marker != "" {
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
// while loading (no marker is shown until a state resolves).
func lyricsMarker(th *theme.Theme, st LyricsState) string {
	switch st {
	case LyricsSynced:
		return th.AccentStyle().Render(noteGlyph + " synced")
	case LyricsUnsynced:
		return th.Muted().Render("unsynced")
	case LyricsNone:
		return th.Muted().Render("no lyrics")
	default:
		return ""
	}
}

// lyricsContent builds the panel's content rows for the given state.
func lyricsContent(th *theme.Theme, st LyricsState, lines []lyrics.Line, plain string, current, innerW, visible int) []string {
	switch st {
	case LyricsNone:
		return []string{th.Muted().Render(Clip("  No lyrics found.", innerW))}
	case LyricsUnsynced:
		return plainLyricsLines(th, plain, innerW, visible)
	case LyricsSynced:
		return syncedLyricsLines(th, lines, current, innerW, visible)
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

// plainLyricsLines renders unsynced text from the top, left-aligned in Primary.
func plainLyricsLines(th *theme.Theme, plain string, innerW, visible int) []string {
	if strings.TrimSpace(plain) == "" {
		return []string{th.Muted().Render(Clip("  No lyrics found.", innerW))}
	}
	src := strings.Split(plain, "\n")
	out := make([]string, 0, visible)
	for i := 0; i < len(src) && i < visible; i++ {
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
