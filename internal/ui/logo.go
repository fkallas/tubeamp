// Package ui — logo header rendered above the panel area.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fkallas/tubeamp/internal/theme"
)

// logoHeight is the number of rows the logo header occupies when visible.
const logoHeight = 2

// logoMinTermHeight is the minimum terminal height that enables the logo.
// Below this value the logo is suppressed so the panel area is not squeezed.
const logoMinTermHeight = 24

// appVersion is displayed in Muted style, right-aligned on the first logo row.
const appVersion = "v0.1.0"

// renderLogo returns the 2-row ASCII logo header, or "" when termHeight is
// below logoMinTermHeight. Each row is padded to exactly termWidth visible
// columns so the result slots cleanly above the panel area in a JoinVertical.
//
// Row 1  — "╭◉╮ tubeamp" (accent glyph, primary wordmark) left-aligned, with
//
//	"v0.1.0" in Muted right-aligned at the far edge.
//
// Row 2  — "╰─╯" in accent + a short decorative rule in Muted (up to ~28
//
//	cols); the already-styled sign-in indicator (when non-empty) is placed
//	at the far right, ANSI-aware-truncated with an ellipsis when it would
//	not fit (a long account name must clip, not vanish); plain spaces pad
//	to termWidth.
func renderLogo(th *theme.Theme, termWidth, termHeight int, indicator string) string {
	if termHeight < logoMinTermHeight {
		return ""
	}
	if termWidth <= 0 {
		return ""
	}

	// ── Row 1 ──────────────────────────────────────────────────────────────
	glyph := th.AccentStyle().Render("╭◉╮")
	wordmark := th.Primary().Render(" tubeamp")
	ver := th.Muted().Render(appVersion)

	left := glyph + wordmark
	leftW := lipgloss.Width(left)
	verW := lipgloss.Width(ver)

	var row1 string
	gap := termWidth - leftW - verW
	if gap >= 1 {
		row1 = left + strings.Repeat(" ", gap) + ver
	} else {
		// Terminal too narrow to show the version tag; just show the wordmark.
		row1 = left
		if lw := lipgloss.Width(row1); lw < termWidth {
			row1 += strings.Repeat(" ", termWidth-lw)
		}
	}

	// ── Row 2 ──────────────────────────────────────────────────────────────
	// A short decorative rule anchored at the left. The accent "╰─╯" (3 cols)
	// plus muted dashes extends up to ~28 cols, then plain spaces fill the rest.
	const ruleMax = 28
	ruleCols := min(ruleMax, termWidth)
	dashCount := ruleCols - 3 // 3 = lipgloss.Width("╰─╯")
	if dashCount < 0 {
		dashCount = 0
	}
	rule := th.AccentStyle().Render("╰─╯") + th.Muted().Render(strings.Repeat("─", dashCount))
	ruleW := lipgloss.Width(rule)
	indW := lipgloss.Width(indicator)

	// A long account name must not silently drop the whole indicator: clip it
	// (ANSI-aware, ellipsis tail) to the columns left of the rule + 1-col gap.
	if avail := termWidth - ruleW - 1; indicator != "" && indW > avail && avail >= 2 {
		indicator = ansi.Truncate(indicator, avail, "…")
		indW = lipgloss.Width(indicator)
	}

	var row2 string
	if indicator != "" && ruleW+1+indW <= termWidth {
		// Right-align the sign-in indicator at the far edge of the rule row.
		row2 = rule + strings.Repeat(" ", termWidth-ruleW-indW) + indicator
	} else {
		row2 = rule
		if rw := lipgloss.Width(row2); rw < termWidth {
			row2 += strings.Repeat(" ", termWidth-rw)
		}
	}

	return row1 + "\n" + row2
}
