// Package ui — logo header rendered above the panel area.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fkallas/tubeamp/internal/theme"
)

// logoHeight is the number of rows the logo header occupies when visible. The
// logo is an "amp with knobs" motif: a small amplifier face (a box-drawn cabinet
// with a row of knobs and a speaker grille) on the left, the "tubeamp" wordmark
// to its right, and a short underline rule beneath the wordmark — three rows.
// Every layout calc that reserves logo rows derives its reservation from this
// constant (see app.go View()).
const logoHeight = 3

// logoMinTermHeight is the minimum terminal height that enables the logo. Below
// this value the logo is suppressed so the panel area is not squeezed. It tracks
// logoHeight: with the fixed chrome (player bar + hint line = 7 rows, see
// View()), a shown logo leaves height−7−logoHeight panel rows, and this
// threshold keeps that minimum at 15 (25−7−3). Raise it in lockstep if
// logoHeight ever grows. (Hiding the logo below the threshold gives those rows
// back to the panels, so the panel area is never smaller than at the threshold.)
const logoMinTermHeight = 25

// appVersion is displayed in Muted style, right-aligned on the first logo row.
const appVersion = "v0.1.0"

// Amp-face glyphs. Knobs come from the round-dial set; the grille is a run of
// heavy verticals reading as a speaker grille. Each glyph is one cell wide and
// the cabinet width is derived from the plain interior string (see renderLogo),
// so the box border always lines up regardless of how the terminal sizes runes.
const (
	logoKnobPower = "◉" // leftmost knob, lit like a power LED (PlayingStyle)
	logoKnobTone  = "◎" // tone dial (AccentStyle)
	logoKnobGain  = "◉" // gain dial (AccentStyle)
	logoGrille    = "┃┃┃"
	logoWordmark  = "tubeamp"
)

// renderLogo returns the 3-row "amp with knobs" logo header, or "" when
// termHeight is below logoMinTermHeight. Each row is padded to exactly termWidth
// visible columns so the result slots cleanly above the panel area in a
// JoinVertical.
//
// The motif is a small amplifier — a box-drawn cabinet with a row of knobs and a
// speaker grille — with the wordmark to its right:
//
//	╭─────────────╮                       v0.1.0
//	│ ◉ ◎ ◉ ┃┃┃ │  tubeamp
//	╰─────────────╯  ───────              ● account
//
// Colours come strictly from theme tokens — the amp cabinet border in
// AccentStyle, the power knob in PlayingStyle (so it glows) with the remaining
// knobs in AccentStyle, the speaker grille + underline rule + version in Muted,
// the wordmark in Primary. ZERO hardcoded colours.
//
// Row 1 — the amp's top edge, with "v0.1.0" in Muted right-aligned at the far
// edge.
// Row 2 — the amp face (knobs + grille) followed by the "tubeamp" wordmark.
// Row 3 — the amp's bottom edge + a short Muted underline rule beneath the
//
//	wordmark; the already-styled sign-in indicator (when non-empty) is placed at
//	the far right, ANSI-aware-truncated with an ellipsis when it would not fit (a
//	long account name must clip, not vanish); plain spaces pad to termWidth.
func renderLogo(th *theme.Theme, termWidth, termHeight int, indicator string) string {
	if termHeight < logoMinTermHeight {
		return ""
	}
	if termWidth <= 0 {
		return ""
	}

	accent := th.AccentStyle()
	muted := th.Muted()

	// Amp cabinet. The interior width is measured from the plain glyph run so the
	// box rules line up no matter how the terminal sizes these runes; the styled
	// face below reproduces the very same glyphs in the same order.
	interior := " " + logoKnobPower + " " + logoKnobTone + " " + logoKnobGain + " " + logoGrille + " "
	bar := strings.Repeat("─", lipgloss.Width(interior))
	ampTop := accent.Render("╭" + bar + "╮")
	ampBot := accent.Render("╰" + bar + "╯")
	ampFace := accent.Render("│") +
		" " + th.PlayingStyle().Render(logoKnobPower) +
		" " + accent.Render(logoKnobTone) +
		" " + accent.Render(logoKnobGain) +
		" " + muted.Render(logoGrille) +
		" " + accent.Render("│")
	ampW := lipgloss.Width(ampTop) // == the visible width of every amp row

	// ── Row 1 — amp top edge + version ───────────────────────────────────────
	ver := muted.Render(appVersion)
	verW := lipgloss.Width(ver)
	var row1 string
	if gap := termWidth - ampW - verW; gap >= 1 {
		row1 = ampTop + strings.Repeat(" ", gap) + ver
	} else {
		// Terminal too narrow to show the version tag; just show the amp top.
		row1 = logoPadRight(ampTop, termWidth)
	}

	// ── Row 2 — amp face + wordmark ──────────────────────────────────────────
	row2 := logoPadRight(ampFace+"  "+th.Primary().Render(logoWordmark), termWidth)

	// ── Row 3 — amp bottom edge + underline rule + sign-in indicator ─────────
	// The rule underlines the wordmark (same width), two cols to the right of the
	// cabinet so it sits directly beneath "tubeamp".
	rule := ampBot + "  " + muted.Render(strings.Repeat("─", lipgloss.Width(logoWordmark)))
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
		row3 = logoPadRight(rule, termWidth)
	}

	return row1 + "\n" + row2 + "\n" + row3
}

// logoPadRight pads s with trailing spaces to exactly w visible columns (no-op
// when s is already at least that wide).
func logoPadRight(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
