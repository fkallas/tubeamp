package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fkallas/tubeamp/internal/lyrics"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// playingWith puts m into a "now playing" state for videoID vid and seeds the
// lyrics cache with a resolved result, without any player or network.
func playingWith(m Model, vid string, r lyricResult) Model {
	m.hasNow = true
	m.nowPlaying = model.Track{VideoID: vid, Title: "A Song", Artists: []string{"An Artist"}}
	m.lyricsCache[vid] = r
	return m
}

// syncedResult builds n timed lyric lines "line NN" at 2s intervals.
func syncedResult(n int) lyricResult {
	lines := make([]lyrics.Line, n)
	for i := range lines {
		lines[i] = lyrics.Line{At: time.Duration(i) * 2 * time.Second, Text: fmt.Sprintf("line %02d", i)}
	}
	return lyricResult{status: lyricResolved, found: true, ly: lyrics.Lyrics{Lines: lines, Synced: true}}
}

// TestLyricsPanelSyncedHighlightAndScroll asserts the synced lyrics render the
// line for the current position and auto-scroll as the position advances.
func TestLyricsPanelSyncedHighlightAndScroll(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", syncedResult(40))

	m.timePos = 1 // current line 0
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "♪ synced") {
		t.Errorf("synced marker missing from lyrics panel:\n%s", v)
	}
	if !strings.Contains(v, "line 00") {
		t.Errorf("view should show the current line \"line 00\":\n%s", v)
	}
	if strings.Contains(v, "line 30") {
		t.Errorf("view should NOT yet show the far line \"line 30\" (window centered on line 0)")
	}

	m.timePos = 61 // current line 30 (At = 60s)
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "line 30") {
		t.Errorf("after advancing, view should show \"line 30\":\n%s", v)
	}
	if strings.Contains(v, "line 00") {
		t.Errorf("after advancing, the window should have scrolled past \"line 00\"")
	}
}

// TestLyricsPanelHiddenWhenIdle: with nothing playing the panel is gone and the
// layout is exactly as before (no lyric content leaks in).
func TestLyricsPanelHiddenWhenIdle(t *testing.T) {
	m := newTestModel(t, 120, 40)
	// Seed a cache entry, but leave hasNow false: the panel must stay hidden.
	m.lyricsCache["v1"] = lyricResult{status: lyricResolved, found: true,
		ly: lyrics.Lyrics{Synced: true, Lines: []lyrics.Line{{Text: "SECRETLYRIC"}}}}
	v := ansi.Strip(m.View())
	if strings.Contains(v, "SECRETLYRIC") || strings.Contains(v, "♪ synced") {
		t.Errorf("idle view must not show the lyrics panel:\n%s", v)
	}
}

// TestLyricsPanelHiddenWhenTooShort: a track is playing but the terminal is too
// short to fit the panel without starving the main view, so it is hidden and the
// main view keeps its rows.
func TestLyricsPanelHiddenWhenTooShort(t *testing.T) {
	m := newTestModel(t, 120, 20) // valid (>= minHeight) but short
	m.hasNow = true
	m.nowPlaying = model.Track{VideoID: "v1", Title: "A Song"}
	m.lyricsCache["v1"] = lyricResult{status: lyricResolved, found: true,
		ly: lyrics.Lyrics{Synced: true, Lines: []lyrics.Line{{Text: "SECRETLYRIC"}}}}
	v := ansi.Strip(m.View())
	if strings.Contains(v, "SECRETLYRIC") || strings.Contains(v, "♪ synced") {
		t.Errorf("too-short view must hide the lyrics panel:\n%s", v)
	}
	if !strings.Contains(v, "Liked Songs") {
		t.Errorf("main view should still render when lyrics are hidden:\n%s", v)
	}
}

// TestLyricsPanelNeverFocusable: the panel is not a focusArea — the 1/2/3/4 and
// h/l controls never reach it; focus only ever cycles the four panels.
func TestLyricsPanelNeverFocusable(t *testing.T) {
	if int(focusCount) != 4 {
		t.Fatalf("focusCount = %d, want 4 (lyrics panel must not be a focusArea)", int(focusCount))
	}
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", syncedResult(10))

	allowed := map[string]bool{"Library": true, "Playlists": true, "Queue": true, "Main": true}
	for _, k := range []string{"1", "2", "3", "4", "l", "l", "l", "l", "h", "h", "h", "h"} {
		m = send(m, runes(k))
		if got := m.FocusedPanel(); !allowed[got] {
			t.Fatalf("after %q focus = %q, want one of the four panels", k, got)
		}
	}
}

// TestLyricsPanelNoLyricsState renders the "No lyrics found." state.
func TestLyricsPanelNoLyricsState(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", lyricResult{status: lyricResolved, found: false})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "No lyrics found.") {
		t.Errorf("expected the no-lyrics notice:\n%s", v)
	}
}

// TestLyricsPanelLoadingState renders the searching notice while a fetch is in
// flight (cache entry present but still loading).
func TestLyricsPanelLoadingState(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", lyricResult{status: lyricLoading})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "searching for lyrics…") {
		t.Errorf("expected the loading notice:\n%s", v)
	}
}

// TestLyricsUnsyncedRendersPlain renders a plain (unsynced) result.
func TestLyricsUnsyncedRendersPlain(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", lyricResult{status: lyricResolved, found: true,
		ly: lyrics.Lyrics{Plain: "PLAINLINE one\nPLAINLINE two", Source: lyrics.SourceYTMusic}})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "PLAINLINE one") || !strings.Contains(v, "unsynced") {
		t.Errorf("expected unsynced plain lyrics with the unsynced marker:\n%s", v)
	}
}

// TestLyricsFetchNilClientNoPanic: with a nil ytm client the fetch path must not
// panic; it dispatches a command that resolves to a lyricsMsg and renders the
// no-lyrics state. The network is never touched (lyricsFetch is stubbed).
func TestLyricsFetchNilClientNoPanic(t *testing.T) {
	var sawClientNil bool
	orig := lyricsFetch
	lyricsFetch = func(_ context.Context, c *ytm.Client, _ model.Track) (lyrics.Lyrics, bool) {
		sawClientNil = c == nil
		return lyrics.Lyrics{}, false // LRCLIB miss + nil-client fallback => none
	}
	defer func() { lyricsFetch = orig }()

	m := newTestModel(t, 120, 40) // nil player, nil client
	m.hasNow = true
	m.nowPlaying = model.Track{VideoID: "v1", Title: "T"}

	cmd := m.ensureLyrics()
	if cmd == nil {
		t.Fatal("ensureLyrics returned no fetch command")
	}
	msg := cmd() // runs the closure -> calls the stub, no network
	lm, ok := msg.(lyricsMsg)
	if !ok {
		t.Fatalf("fetch command produced %T, want lyricsMsg", msg)
	}
	if !sawClientNil {
		t.Errorf("fetch was not given a nil client")
	}
	m = send(m, lm)
	if !strings.Contains(ansi.Strip(m.View()), "No lyrics found.") {
		t.Errorf("nil-client resolution should render the no-lyrics state")
	}
}

// TestLyricsStaleResultIgnored: a lyrics result for a generation we have moved
// past — and a track no longer playing — is dropped.
func TestLyricsStaleResultIgnored(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m.hasNow = true
	m.nowPlaying = model.Track{VideoID: "current"}
	m.lyricsGen = 5
	m.lyricsCache["current"] = lyricResult{status: lyricLoading}

	// A late result for an old track at an old generation must be ignored.
	m = send(m, lyricsMsg{gen: 4, videoID: "old", found: true,
		ly: lyrics.Lyrics{Plain: "old lyrics"}})
	if _, ok := m.lyricsCache["old"]; ok {
		t.Errorf("stale lyrics result for a previous track should be dropped")
	}

	// A result for the current track is recorded even if its generation is stale
	// (e.g. switched away and back while the fetch was in flight).
	m = send(m, lyricsMsg{gen: 4, videoID: "current", found: true,
		ly: lyrics.Lyrics{Plain: "cur lyrics"}})
	if r, ok := m.lyricsCache["current"]; !ok || r.status != lyricResolved {
		t.Errorf("current-track result should be recorded; got %+v", r)
	}
}

// TestLayoutInvariantWithLyricsPanel: with the lyrics panel visible the rendered
// view still totals exactly the terminal height with full-width rows at every
// size in the visible band.
func TestLayoutInvariantWithLyricsPanel(t *testing.T) {
	for _, w := range []int{minWidth, 120} {
		for h := 21; h <= 30; h++ {
			m := newTestModel(t, w, h)
			m = playingWith(m, "v1", syncedResult(40))
			m.timePos = 20
			lines := strings.Split(m.View(), "\n")
			if len(lines) != h {
				t.Errorf("%dx%d: view has %d rows, want %d", w, h, len(lines), h)
				continue
			}
			for i, ln := range lines {
				if got := lipgloss.Width(ln); got != w {
					t.Errorf("%dx%d: line %d width = %d, want %d", w, h, i, got, w)
					break
				}
			}
		}
	}
}
