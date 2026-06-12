package panels

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/fkallas/tubeamp/internal/lyrics"
	"github.com/fkallas/tubeamp/internal/theme"
)

// TestLyricsViewDimensions: every state renders exactly w columns by h rows.
func TestLyricsViewDimensions(t *testing.T) {
	th := theme.Default()
	lines := []lyrics.Line{
		{At: 0, Text: "first"},
		{At: 2 * time.Second, Text: "second"},
		{At: 4 * time.Second, Text: ""}, // a blank/instrumental cue
		{At: 6 * time.Second, Text: "fourth"},
	}
	cases := []struct {
		name string
		out  string
	}{
		{"loading", LyricsView(th, LyricsLoading, nil, "", -1, 0, false, false, 50, 8)},
		{"none", LyricsView(th, LyricsNone, nil, "", -1, 0, false, false, 50, 8)},
		{"synced", LyricsView(th, LyricsSynced, lines, "", 2, 0, false, false, 50, 8)},
		{"unsynced", LyricsView(th, LyricsUnsynced, nil, "a\nb\nc", -1, 0, false, false, 50, 8)},
	}
	for _, tc := range cases {
		rows := strings.Split(tc.out, "\n")
		if len(rows) != 8 {
			t.Errorf("%s: %d rows, want 8", tc.name, len(rows))
			continue
		}
		for i, r := range rows {
			if got := lipgloss.Width(r); got != 50 {
				t.Errorf("%s: row %d width = %d, want 50", tc.name, i, got)
				break
			}
		}
	}
}

// TestLyricsViewSyncedShowsCurrent: the synced view shows the current line and
// labels itself "synced".
func TestLyricsViewSyncedShowsCurrent(t *testing.T) {
	th := theme.Default()
	lines := []lyrics.Line{{At: 0, Text: "alpha"}, {At: 2 * time.Second, Text: "bravo"}}
	v := ansi.Strip(LyricsView(th, LyricsSynced, lines, "", 1, 0, false, false, 40, 6))
	if !strings.Contains(v, "bravo") || !strings.Contains(v, "♪ synced") {
		t.Errorf("synced view missing current line or marker:\n%s", v)
	}
}

// TestLyricsViewDetachedMarker: a detached (peek-scrolled) synced panel shows the
// paused-follow marker and centers on the peeked line rather than the live one.
func TestLyricsViewDetachedMarker(t *testing.T) {
	th := theme.Default()
	lines := []lyrics.Line{
		{At: 0, Text: "alpha"}, {At: 2 * time.Second, Text: "bravo"},
		{At: 4 * time.Second, Text: "charlie"}, {At: 6 * time.Second, Text: "delta"},
	}
	// current=0 (live line) but detached scroll=3 (peeked): the panel shows the
	// peeked line and the paused marker, not "♪ synced".
	v := ansi.Strip(LyricsView(th, LyricsSynced, lines, "", 0, 3, true, true, 40, 8))
	if !strings.Contains(v, "delta") {
		t.Errorf("detached view should center on the peeked line \"delta\":\n%s", v)
	}
	if !strings.Contains(v, "paused") || strings.Contains(v, "♪ synced") {
		t.Errorf("detached view should show the paused marker, not the synced one:\n%s", v)
	}
}

// TestLyricsViewActiveBorderDiffers: a focused (active) lyrics panel renders the
// active border, so its output differs from the unfocused panel — but only in
// styling, not content (the border glyphs are identical). The colour profile is
// forced so the active/inactive border colours are observable; the test does not
// assert any specific ANSI code, only that focus changes the rendering.
func TestLyricsViewActiveBorderDiffers(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	th := theme.Default()
	lines := []lyrics.Line{{At: 0, Text: "alpha"}}
	active := LyricsView(th, LyricsSynced, lines, "", 0, 0, false, true, 40, 6)
	inactive := LyricsView(th, LyricsSynced, lines, "", 0, 0, false, false, 40, 6)
	if active == inactive {
		t.Errorf("focused lyrics panel should render a different (active) border than unfocused")
	}
	if ansi.Strip(active) != ansi.Strip(inactive) {
		t.Errorf("active vs inactive lyrics panels should differ only in styling, not content")
	}
}
