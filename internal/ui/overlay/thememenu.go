package overlay

import (
	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ui/panels"
)

// ThemeMenu is the theme-picker overlay. It tracks the available theme names
// and the highlighted cursor; the root model owns the loaded Theme objects and
// applies the live preview as the cursor moves.
type ThemeMenu struct {
	Names   []string
	Cursor  int
	Loading bool
}

// Up moves the cursor to the previous theme.
func (t *ThemeMenu) Up() {
	if t.Cursor > 0 {
		t.Cursor--
	}
}

// Down moves the cursor to the next theme.
func (t *ThemeMenu) Down() {
	if t.Cursor < len(t.Names)-1 {
		t.Cursor++
	}
}

// Selected returns the highlighted theme name, or "" when the list is empty.
func (t ThemeMenu) Selected() string {
	if t.Cursor < 0 || t.Cursor >= len(t.Names) {
		return ""
	}
	return t.Names[t.Cursor]
}

// View renders the theme-picker box sized to fit within maxW×maxH.
func (t ThemeMenu) View(th *theme.Theme, maxW, maxH int) string {
	w := 36
	if w > maxW-4 {
		w = maxW - 4
	}
	innerW := w - 2

	var body []string
	switch {
	case t.Loading:
		body = []string{th.Muted().Render("loading themes…")}
	case len(t.Names) == 0:
		body = []string{th.Muted().Render("no themes found")}
	default:
		visible := maxH - 6
		if visible < 1 {
			visible = 1
		}
		start, end := windowRange(t.Cursor, len(t.Names), visible)
		for i := start; i < end; i++ {
			name := t.Names[i]
			cursor := "  "
			if i == t.Cursor {
				cursor = "> "
			}
			if i == t.Cursor {
				body = append(body, th.Selected().Render(panels.PadPlain(cursor+name, innerW)))
			} else {
				body = append(body, cursor+th.Primary().Render(panels.Clip(name, innerW-2)))
			}
		}
	}

	return panels.Box(th, "Themes", body, w, len(body)+2, true)
}

// windowRange keeps cursor visible within visible rows of total items.
func windowRange(cursor, total, visible int) (int, int) {
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
