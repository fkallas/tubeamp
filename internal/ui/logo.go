// Package ui — wordmark header rendered above the panel area.
package ui

import (
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

// logoWordmark is the literal word drawn in the top-left, rendered in the
// theme's accent colour (see renderWordmark).
const logoWordmark = "tubeamp"

// renderWordmark draws the "tubeamp" wordmark in a single colour: the theme's
// accent — the same hue that highlights the active panel's border. Colour comes
// strictly from a theme token; no hardcoded hex.
func renderWordmark(th *theme.Theme) string {
	return th.AccentStyle().Render(logoWordmark)
}

// renderLogo returns the single-row wordmark header padded to exactly termWidth
// visible columns so it slots cleanly above the panel area in a JoinVertical: the
// accent-coloured "tubeamp" wordmark on the left, then the Muted version tag and
// the already-styled sign-in indicator right-aligned at the far edge (the
// indicator's home today).
//
// A long account name must not silently drop the whole indicator nor widen the
// row past termWidth (which would make JoinVertical pad every frame row and break
// the full-width frame invariant): it is ANSI-aware-truncated with an ellipsis
// to the columns left of the wordmark + version + the two gaps.
func renderLogo(th *theme.Theme, termWidth int, indicator string) string {
	if termWidth <= 0 {
		return ""
	}

	word := renderWordmark(th)
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
