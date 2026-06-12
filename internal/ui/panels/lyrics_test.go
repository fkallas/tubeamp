package panels

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
		{"loading", LyricsView(th, LyricsLoading, nil, "", -1, 50, 8)},
		{"none", LyricsView(th, LyricsNone, nil, "", -1, 50, 8)},
		{"synced", LyricsView(th, LyricsSynced, lines, "", 2, 50, 8)},
		{"unsynced", LyricsView(th, LyricsUnsynced, nil, "a\nb\nc", -1, 50, 8)},
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
	v := ansi.Strip(LyricsView(th, LyricsSynced, lines, "", 1, 40, 6))
	if !strings.Contains(v, "bravo") || !strings.Contains(v, "♪ synced") {
		t.Errorf("synced view missing current line or marker:\n%s", v)
	}
}
