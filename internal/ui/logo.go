// Package ui — logo header rendered above the panel area.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fkallas/tubeamp/internal/theme"
)

// logoHeight is the number of rows the logo header occupies when visible. The
// cyberpunk wordmark is a 3-row block: a glitch emblem cap, the wordmark line,
// and an underline rule. Every layout calc that reserves logo rows derives its
// reservation from this constant (see app.go View()).
const logoHeight = 3

// logoMinTermHeight is the minimum terminal height that enables the logo. Below
// this value the logo is suppressed so the panel area is not squeezed. It tracks
// logoHeight: with the fixed chrome (player bar + hint line = 7 rows, see
// View()), a shown logo leaves height−7−logoHeight panel rows, and this
// threshold keeps that minimum at 15 (25−7−3) — the same floor the previous
// 2-row logo had at its 24 threshold. Raise it in lockstep if logoHeight ever
// grows. (Hiding the logo below the threshold gives those rows back to the
// panels, so the panel area is never smaller than at the threshold.)
const logoMinTermHeight = 25

// appVersion is displayed in Muted style, right-aligned on the first logo row.
const appVersion = "v0.1.0"

// renderLogo returns the 3-row cyberpunk logo header, or "" when termHeight is
// below logoMinTermHeight. Each row is padded to exactly termWidth visible
// columns so the result slots cleanly above the panel area in a JoinVertical.
//
// The wordmark is a neon "play button in a glitch frame":
//
//	▛▀▜                                    v0.1.0
//	▌▶▐ tubeamp ▓▒░
//	▙▄▟━━━━━━━━━━━━━━━━━            ● account
//
// Colours come strictly from theme tokens — the emblem + glitch gradient in
// AccentStyle (neon magenta), the ▶ play glyph in PlayingStyle (neon green so it
// glows), the wordmark in Primary, the underline rule + version in Muted. ZERO
// hardcoded colours.
//
// Row 1 — the emblem cap, with "v0.1.0" in Muted right-aligned at the far edge.
// Row 2 — the wordmark line (emblem body + ▶ + "tubeamp" + glitch tail).
// Row 3 — the emblem foot + a short Muted underline rule (up to ~28 cols); the
//
//	already-styled sign-in indicator (when non-empty) is placed at the far
//	right, ANSI-aware-truncated with an ellipsis when it would not fit (a long
//	account name must clip, not vanish); plain spaces pad to termWidth.
func renderLogo(th *theme.Theme, termWidth, termHeight int, indicator string) string {
	if termHeight < logoMinTermHeight {
		return ""
	}
	if termWidth <= 0 {
		return ""
	}

	accent := th.AccentStyle()
	muted := th.Muted()

	// ── Row 1 — emblem cap + version ─────────────────────────────────────────
	emblemCap := accent.Render("▛▀▜")
	ver := muted.Render(appVersion)
	capW := lipgloss.Width(emblemCap)
	verW := lipgloss.Width(ver)

	var row1 string
	if gap := termWidth - capW - verW; gap >= 1 {
		row1 = emblemCap + strings.Repeat(" ", gap) + ver
	} else {
		// Terminal too narrow to show the version tag; just show the emblem cap.
		row1 = emblemCap
		if w := lipgloss.Width(row1); w < termWidth {
			row1 += strings.Repeat(" ", termWidth-w)
		}
	}

	// ── Row 2 — wordmark line ────────────────────────────────────────────────
	// "▌▶▐" reads as a neon play button: the ▶ (PlayingStyle, glowing) framed by
	// accent half-blocks. The "▓▒░" tail is a glitch dissolve to the right.
	wordmark := accent.Render("▌") +
		th.PlayingStyle().Render("▶") +
		accent.Render("▐") +
		th.Primary().Render(" tubeamp ") +
		accent.Render("▓▒░")
	row2 := wordmark
	if w := lipgloss.Width(row2); w < termWidth {
		row2 += strings.Repeat(" ", termWidth-w)
	}

	// ── Row 3 — emblem foot + underline rule + sign-in indicator ─────────────
	// The accent "▙▄▟" (3 cols) plus a Muted heavy underline extends up to ~28
	// cols, then plain spaces fill the rest.
	const ruleMax = 28
	ruleCols := min(ruleMax, termWidth)
	dashCount := ruleCols - 3 // 3 = lipgloss.Width("▙▄▟")
	if dashCount < 0 {
		dashCount = 0
	}
	rule := accent.Render("▙▄▟") + muted.Render(strings.Repeat("━", dashCount))
	ruleW := lipgloss.Width(rule)
	indW := lipgloss.Width(indicator)

	// A long account name must not silently drop the whole indicator: clip it
	// (ANSI-aware, ellipsis tail) to the columns left of the rule + 1-col gap.
	if avail := termWidth - ruleW - 1; indicator != "" && indW > avail && avail >= 2 {
		indicator = ansi.Truncate(indicator, avail, "…")
		indW = lipgloss.Width(indicator)
	}

	var row3 string
	if indicator != "" && ruleW+1+indW <= termWidth {
		// Right-align the sign-in indicator at the far edge of the rule row.
		row3 = rule + strings.Repeat(" ", termWidth-ruleW-indW) + indicator
	} else {
		row3 = rule
		if w := lipgloss.Width(row3); w < termWidth {
			row3 += strings.Repeat(" ", termWidth-w)
		}
	}

	return row1 + "\n" + row2 + "\n" + row3
}
