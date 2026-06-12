package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// sgr wraps s in an SGR foreground colour so tests exercise styled input the way
// the real overlays (lipgloss-rendered) do.
func sgr(code, s string) string { return "\x1b[" + code + "m" + s + "\x1b[m" }

func TestCompositePreservesBothSides(t *testing.T) {
	// 20-column styled background; styled overlay spliced in the middle.
	bg := sgr("31", "0123456789") + sgr("32", "ABCDEFGHIJ") // width 20
	if w := ansi.StringWidth(bg); w != 20 {
		t.Fatalf("background width = %d, want 20", w)
	}
	ol := sgr("34", "[OVL]") // width 5
	out := Composite(ol, bg, 8, 0)

	plain := ansi.Strip(out)
	// cols [0,8) from bg, then overlay at [8,13), then bg cols [13,20).
	if want := "01234567[OVL]DEFGHIJ"; plain != want {
		t.Fatalf("composite plain = %q, want %q", plain, want)
	}
	if w := ansi.StringWidth(out); w != 20 {
		t.Errorf("composite width = %d, want 20 (must match background)", w)
	}
	// Left/right background segments must keep their colours: the red SGR and the
	// green SGR from the background both survive the splice.
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("left background colour (31) lost; out = %q", out)
	}
	if !strings.Contains(out, "\x1b[32m") {
		t.Errorf("right background colour (32) lost; out = %q", out)
	}
	if !strings.Contains(out, "\x1b[34m") {
		t.Errorf("overlay colour (34) lost; out = %q", out)
	}
}

func TestCompositeLandsAtColumn(t *testing.T) {
	bg := strings.Repeat(".", 30)
	ol := "XYZ"
	for _, x := range []int{0, 1, 13, 27} {
		out := ansi.Strip(Composite(ol, bg, x, 0))
		// Overlay content begins exactly at column x.
		if got := out[x : x+3]; got != "XYZ" {
			t.Errorf("x=%d: overlay landed at %q, want XYZ", x, got)
		}
		// Everything else is untouched background.
		if out[:x] != strings.Repeat(".", x) {
			t.Errorf("x=%d: left segment corrupted: %q", x, out[:x])
		}
		if out[x+3:] != strings.Repeat(".", 30-x-3) {
			t.Errorf("x=%d: right segment corrupted: %q", x, out[x+3:])
		}
		if len(out) != 30 {
			t.Errorf("x=%d: width = %d, want 30", x, len(out))
		}
	}
}

func TestCompositeMultiRow(t *testing.T) {
	bg := strings.Join([]string{
		strings.Repeat("a", 10),
		strings.Repeat("b", 10),
		strings.Repeat("c", 10),
		strings.Repeat("d", 10),
	}, "\n")
	ol := "##\n##"
	out := Composite(ol, bg, 4, 1)
	lines := strings.Split(ansi.Strip(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("rows = %d, want 4", len(lines))
	}
	if lines[0] != "aaaaaaaaaa" {
		t.Errorf("row 0 should be untouched, got %q", lines[0])
	}
	if lines[1] != "bbbb##bbbb" {
		t.Errorf("row 1 = %q, want bbbb##bbbb", lines[1])
	}
	if lines[2] != "cccc##cccc" {
		t.Errorf("row 2 = %q, want cccc##cccc", lines[2])
	}
	if lines[3] != "dddddddddd" {
		t.Errorf("row 3 should be untouched, got %q", lines[3])
	}
}

func TestCompositeWideRuneLeftBoundary(t *testing.T) {
	// "漢" occupies cols 8-9; splicing at col 9 bisects it. The bisected wide
	// rune in the left segment must become a single space, keeping width exact.
	bg := strings.Repeat("x", 8) + "漢" + strings.Repeat("y", 8) // width 18
	if w := ansi.StringWidth(bg); w != 18 {
		t.Fatalf("bg width = %d, want 18", w)
	}
	ol := "OO"
	out := Composite(ol, bg, 9, 0)
	if w := ansi.StringWidth(out); w != 18 {
		t.Errorf("width = %d, want 18 (bisected wide rune must not corrupt)", w)
	}
	plain := ansi.Strip(out)
	// col 8 (first half of 漢) -> space; overlay covers cols [9,11) which includes
	// col 10 (one y), so the right segment keeps the remaining 7 y's.
	if want := "xxxxxxxx OOyyyyyyy"; plain != want {
		t.Errorf("plain = %q, want %q", plain, want)
	}
}

func TestCompositeWideRuneRightBoundary(t *testing.T) {
	// "漢" occupies cols 8-9; overlay ends exactly at col 9, so the wide rune is
	// bisected on the right seam and must become a space.
	bg := strings.Repeat("x", 8) + "漢" + strings.Repeat("y", 8) // width 18
	ol := "OOOOOOO"                                             // width 7, placed at col 2 -> ends at col 9
	out := Composite(ol, bg, 2, 0)
	if w := ansi.StringWidth(out); w != 18 {
		t.Errorf("width = %d, want 18", w)
	}
	plain := ansi.Strip(out)
	// cols [0,2)=xx, overlay [2,9)=OOOOOOO, col 9 was the second half of 漢 ->
	// space, then yyyyyyyy.
	if want := "xxOOOOOOO yyyyyyyy"; plain != want {
		t.Errorf("plain = %q, want %q", plain, want)
	}
}

func TestCompositeClampsOverlayWiderThanBackground(t *testing.T) {
	bg := strings.Repeat(".", 10)
	ol := strings.Repeat("#", 40) // far wider than the background
	out := Composite(ol, bg, 0, 0)
	if w := ansi.StringWidth(out); w != 10 {
		t.Errorf("clamped width = %d, want 10", w)
	}
	if plain := ansi.Strip(out); plain != strings.Repeat("#", 10) {
		t.Errorf("clamped overlay = %q, want 10 #s", plain)
	}
}

func TestCompositeCenterClampsTallerOverlay(t *testing.T) {
	bg := strings.Join([]string{"....", "....", "...."}, "\n") // 3 rows of 4
	ol := strings.Join([]string{"##", "##", "##", "##", "##"}, "\n")
	out := CompositeCenter(ol, bg)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Errorf("rows = %d, want 3 (overlay taller than bg must be clamped)", len(lines))
	}
	for i, ln := range lines {
		if w := ansi.StringWidth(ln); w != 4 {
			t.Errorf("row %d width = %d, want 4", i, w)
		}
	}
}

func TestCompositeShortBackgroundPads(t *testing.T) {
	// Background row is only 3 wide but the overlay is anchored at col 6: the gap
	// is padded with spaces so the overlay still lands at the requested column.
	bg := "abc"
	out := ansi.Strip(Composite("##", bg, 6, 0))
	if want := "abc   ##"; out != want {
		t.Errorf("short-bg pad = %q, want %q", out, want)
	}
}

func TestCompositeCenterPositions(t *testing.T) {
	bg := strings.Join([]string{
		strings.Repeat(".", 11),
		strings.Repeat(".", 11),
		strings.Repeat(".", 11),
	}, "\n")
	ol := "###" // width 3
	out := ansi.Strip(CompositeCenter(ol, bg))
	lines := strings.Split(out, "\n")
	// Centered in 11 wide -> x=(11-3)/2=4; centered in 3 rows -> y=(3-1)/2=1.
	if lines[1][4:7] != "###" {
		t.Errorf("overlay not centered horizontally: %q", lines[1])
	}
	if strings.Contains(lines[0], "#") || strings.Contains(lines[2], "#") {
		t.Errorf("overlay not centered vertically: %v", lines)
	}
}
