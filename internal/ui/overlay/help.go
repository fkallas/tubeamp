// Package overlay implements tubeamp's modal overlays (help, search, theme
// picker). Each overlay renders a rounded-border box using the active theme; the
// root model composites it (centered, ANSI-aware) over the rendered app view via
// ui.CompositeCenter and routes all keys to the open overlay until esc closes it.
package overlay

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ui/keymap"
	"github.com/fkallas/tubeamp/internal/ui/panels"
)

// helpColGap is the blank gap between columns in the multi-column help layout.
const helpColGap = 2

// Help is the keybinding reference overlay. It is stateless; View renders the
// current key map.
type Help struct{}

// View renders the help overlay box sized to fit within maxW×maxH. When the
// full binding list is taller than the box, the bindings are packed into as
// many equal-height columns as the width allows (rather than truncating most of
// them to the first column), with a "…" indicator if even that overflows.
func (Help) View(th *theme.Theme, km keymap.KeyMap, maxW, maxH int) string {
	groups := km.FullHelp()
	titles := keymap.HelpGroupTitles()

	var lines []string
	for gi, g := range groups {
		if gi < len(titles) {
			lines = append(lines, th.AccentStyle().Render(titles[gi]))
		}
		for _, b := range g {
			h := b.Help()
			lines = append(lines, "  "+th.Primary().Render(panels.PadPlain(h.Key, 12))+th.Muted().Render(h.Desc))
		}
		lines = append(lines, "")
	}
	// Drop the trailing blank separator; it only wastes a row.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	// Content area available inside the box border.
	contentH := maxH - 2
	if contentH < 1 {
		contentH = 1
	}
	availW := maxW - 4
	if availW < 10 {
		availW = 10
	}

	// Single column fits the height: render as-is.
	if len(lines) <= contentH {
		w := colWidth(lines) + 2
		if w > availW {
			w = availW
		}
		return panels.Box(th, "Help", lines, w, len(lines)+2, true)
	}

	body, boxW := packColumns(th, lines, contentH, availW)
	return panels.Box(th, "Help", body, boxW+2, len(body)+2, true)
}

// colWidth returns the widest display width among lines (ANSI-aware).
func colWidth(lines []string) int {
	w := 0
	for _, ln := range lines {
		if x := lipgloss.Width(ln); x > w {
			w = x
		}
	}
	return w
}

// packColumns lays lines out column-major into equal-height columns (each at
// most contentH rows) that fit within availW, returning the composed rows and
// their total display width. If the lines still overflow, the last visible cell
// becomes a muted "…" indicator so the user knows to resize for the rest.
func packColumns(th *theme.Theme, lines []string, contentH, availW int) ([]string, int) {
	cw := colWidth(lines)
	if cw < 1 {
		cw = 1
	}
	colsFit := (availW + helpColGap) / (cw + helpColGap)
	if colsFit < 1 {
		colsFit = 1
	}
	colsNeeded := (len(lines) + contentH - 1) / contentH
	cols := colsFit
	if colsNeeded < cols {
		cols = colsNeeded
	}

	rows := (len(lines) + cols - 1) / cols
	if rows > contentH {
		rows = contentH
	}
	capacity := cols * rows
	visible := lines
	if len(visible) > capacity {
		// Copy before mutating so we never clobber the caller's slice.
		visible = append([]string(nil), lines[:capacity]...)
		visible[capacity-1] = th.Muted().Render("…")
	}

	body := make([]string, rows)
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		for c := 0; c < cols; c++ {
			if c > 0 {
				sb.WriteString(strings.Repeat(" ", helpColGap))
			}
			idx := c*rows + r
			cell := ""
			if idx < len(visible) {
				cell = visible[idx]
			}
			sb.WriteString(panels.PadPlain(cell, cw))
		}
		body[r] = sb.String()
	}
	return body, cols*cw + (cols-1)*helpColGap
}
