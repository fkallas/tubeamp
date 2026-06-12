// Package ui — wordmark header rendered above the panel area.
package ui

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fkallas/tubeamp/internal/theme"
)

// logoHeight is the number of rows the wordmark header occupies. The header is a
// single row: the colour-cycling "tubeamp" wordmark in the top-left, with the
// version tag and sign-in indicator right-aligned. Every layout calc that
// reserves header rows derives its reservation from this constant (see app.go
// View()).
const logoHeight = 1

// logoMinTermHeight is the minimum terminal height that enables the header. The
// panel area has a floor (the Library panel + a 6-row Playlists/Queue minimum =
// 13 rows) and the fixed chrome is 7 rows (player bar + hint line), so the
// smallest renderable terminal is minHeight (20) rows. Adding the 1-row header
// needs one extra row of headroom, so the header is suppressed only at the very
// shortest height (where it would not fit) and the panel area reclaims that row.
const logoMinTermHeight = minHeight + logoHeight

// appVersion is displayed in Muted style, right-aligned on the header row.
const appVersion = "v0.1.0"

// logoWordmark is the literal word drawn in the top-left. Each letter is tinted
// with a theme-palette colour, and the colours cycle across the letters as the
// animation phase advances (see renderWordmark).
const logoWordmark = "tubeamp"

// renderWordmark draws the "tubeamp" wordmark with each letter coloured from the
// theme palette: letter i takes palette[(i+phase) % len(palette)], so advancing
// the phase flows the colours across the word over time. Colours come strictly
// from theme tokens — ZERO hardcoded hex. The visible width is always
// len(logoWordmark) columns regardless of phase, so the layout never shifts.
func renderWordmark(th *theme.Theme, phase int) string {
	pal := th.Palette()
	if len(pal) == 0 {
		// No palette tokens parsed: fall back to the primary text style.
		return th.Primary().Render(logoWordmark)
	}
	if phase < 0 {
		phase = -phase
	}
	var b strings.Builder
	for i, r := range logoWordmark {
		c := pal[(i+phase)%len(pal)]
		b.WriteString(lipgloss.NewStyle().Foreground(lipglossColor(c)).Render(string(r)))
	}
	return b.String()
}

// lipglossColor converts an image/color.Color (the form theme.Palette() returns)
// to a lipgloss colour via its "#rrggbb" hex string.
func lipglossColor(c color.Color) lipgloss.Color {
	r, g, b, _ := c.RGBA()
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8)))
}

// renderLogo returns the single-row wordmark header padded to exactly termWidth
// visible columns so it slots cleanly above the panel area in a JoinVertical: the
// colour-cycling "tubeamp" wordmark on the left, then the Muted version tag and
// the already-styled sign-in indicator right-aligned at the far edge (the
// indicator's home today). phase drives the colour animation (see renderWordmark).
//
// A long account name must not silently drop the whole indicator nor widen the
// row past termWidth (which would make JoinVertical pad every frame row and break
// the full-width frame invariant): it is ANSI-aware-truncated with an ellipsis
// to the columns left of the wordmark + version + the two gaps.
func renderLogo(th *theme.Theme, termWidth, phase int, indicator string) string {
	if termWidth <= 0 {
		return ""
	}

	word := renderWordmark(th, phase)
	wordW := lipgloss.Width(word)

	ver := th.Muted().Render(appVersion)
	verW := lipgloss.Width(ver)

	// Right cluster: the version tag, then the sign-in indicator (when present)
	// two columns to its right, the whole cluster pushed to the far edge.
	right := ver
	rightW := verW
	if indicator != "" {
		indW := lipgloss.Width(indicator)
		// Clip an oversized indicator (ANSI-aware, ellipsis tail) so the row never
		// exceeds termWidth: leave the wordmark + a 1-col gap + version + a 2-col
		// gap before it.
		if avail := termWidth - wordW - 1 - verW - 2; indW > avail && avail >= 2 {
			indicator = ansi.Truncate(indicator, avail, "…")
			indW = lipgloss.Width(indicator)
		}
		right = ver + "  " + indicator
		rightW = verW + 2 + indW
	}

	if gap := termWidth - wordW - rightW; gap >= 1 {
		return word + strings.Repeat(" ", gap) + right
	}
	// Terminal too narrow for the right cluster: just show the wordmark, padded.
	return logoPadRight(word, termWidth)
}

// logoPadRight pads s with trailing spaces to exactly w visible columns (no-op
// when s is already at least that wide).
func logoPadRight(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
