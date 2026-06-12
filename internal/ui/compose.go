package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Composite splices overlay over background, anchoring the overlay's top-left
// corner at column x, row y, and returns the merged frame. Unlike padding an
// overlay to full width with spaces (which blanks whole rows), this keeps the
// background visible on BOTH sides of the overlay: for every overlay row it
// keeps background columns [0,x), drops in the overlay row, then keeps
// background columns [x+w,…), where w is that overlay row's display width.
//
// It is ANSI-aware: SGR (colour/attribute) state on each side is cut and
// restored so styles never bleed across the seams, and a wide rune (CJK/emoji)
// bisected by either seam is replaced with a single space rather than corrupting
// the line. Background rows the overlay does not cover are returned verbatim.
//
// Overlays wider than the background row are clamped to fit; rows past the
// bottom of the background are dropped. Negative offsets are clamped to zero,
// and a background row shorter than x is padded with spaces up to x.
func Composite(overlay, background string, x, y int) string {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	bgLines := strings.Split(background, "\n")
	olLines := strings.Split(overlay, "\n")
	for i, ol := range olLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bgLines[row] = spliceLine(bgLines[row], ol, x)
	}
	return strings.Join(bgLines, "\n")
}

// CompositeCenter splices overlay centered (both axes) over background. The
// overlay is measured by its widest row and total height; offsets are clamped so
// an overlay larger than the background is pinned to the top-left and then
// clamped per row by Composite.
func CompositeCenter(overlay, background string) string {
	bgLines := strings.Split(background, "\n")
	olLines := strings.Split(overlay, "\n")

	bgW := maxLineWidth(bgLines)
	olW := maxLineWidth(olLines)

	x := (bgW - olW) / 2
	if x < 0 {
		x = 0
	}
	y := (len(bgLines) - len(olLines)) / 2
	if y < 0 {
		y = 0
	}
	return Composite(overlay, background, x, y)
}

// spliceLine merges one overlay row into one background row at column x.
func spliceLine(bg, ol string, x int) string {
	if x < 0 {
		x = 0
	}
	bgW := ansi.StringWidth(bg)

	// Left: background columns [0, x). Truncate keeps inline styles; a wide rune
	// straddling the seam is dropped (its cell becomes the pad space below). The
	// reset closes any style left open by the cut so it can't bleed into the
	// overlay or the pad.
	left := ansi.Truncate(bg, x, "") + ansi.ResetStyle
	if lw := ansi.StringWidth(left); lw < x {
		left += strings.Repeat(" ", x-lw)
	}

	// Clamp the overlay so it never extends past the background width (only when
	// the background actually reaches x — a short background is padded above and
	// the overlay is allowed to extend the row).
	ow := ansi.StringWidth(ol)
	if bgW > x {
		if maxOl := bgW - x; ow > maxOl {
			ol = ansi.Truncate(ol, maxOl, "")
			ow = ansi.StringWidth(ol)
		}
	}
	// Reset after the overlay so its trailing style can't bleed into the right
	// background segment.
	body := ol + ansi.ResetStyle

	// Right: background columns [x+ow, …). TruncateLeft preserves the SGR state
	// active at the seam, so the right segment keeps its colour. If a wide rune
	// straddles the seam TruncateLeft over-includes it by one cell; detect that
	// and drop it, padding the bisected cell with a space.
	right := ""
	if rightStart := x + ow; rightStart < bgW {
		right = ansi.TruncateLeft(bg, rightStart, "")
		if ansi.StringWidth(right) > bgW-rightStart {
			right = " " + ansi.TruncateLeft(bg, rightStart+1, "")
		}
	}

	return left + body + right
}

// maxLineWidth returns the widest display width among lines (ANSI-aware).
func maxLineWidth(lines []string) int {
	w := 0
	for _, ln := range lines {
		if x := ansi.StringWidth(ln); x > w {
			w = x
		}
	}
	return w
}
