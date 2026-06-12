package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fkallas/tubeamp/internal/lyrics"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/player"
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

// unsyncedResult builds an n-line plain (unsynced) lyrics result.
func unsyncedResult(n int) lyricResult {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("PLAIN %02d", i)
	}
	return lyricResult{status: lyricResolved, found: true,
		ly: lyrics.Lyrics{Plain: strings.Join(lines, "\n"), Source: lyrics.SourceYTMusic}}
}

// focusLyricsPanel cycles 'l' until the lyrics panel is focused (or fails).
func focusLyricsPanel(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < int(focusCount)+1; i++ {
		if m.FocusedPanel() == "Lyrics" {
			return m
		}
		m = send(m, runes("l"))
	}
	t.Fatalf("h/l cycle never reached the Lyrics panel; focus stuck at %q", m.FocusedPanel())
	return m
}

// TestLyricsHiddenNeverFocusable: while the lyrics panel is hidden (nothing
// playing) the h/l cycle and the 1/2/3/4 keys never land on it; focus only ever
// reaches the four always-visible panels.
func TestLyricsHiddenNeverFocusable(t *testing.T) {
	m := newTestModel(t, 120, 40) // nothing playing => lyrics hidden
	allowed := map[string]bool{"Library": true, "Playlists": true, "Queue": true, "Main": true}
	for _, k := range []string{"1", "2", "3", "4", "l", "l", "l", "l", "l", "h", "h", "h", "h", "h"} {
		m = send(m, runes(k))
		if got := m.FocusedPanel(); !allowed[got] {
			t.Fatalf("after %q focus = %q, want one of the four panels (lyrics is hidden)", k, got)
		}
	}
}

// TestLyricsFocusReachableWhenVisible: with a visible lyrics panel the h/l cycle
// reaches focusLyrics, and the number keys still only address the four panels.
func TestLyricsFocusReachableWhenVisible(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", syncedResult(10))

	// l cycles Library→Playlists→Queue→Main→Lyrics→Library.
	for _, want := range []string{"Playlists", "Queue", "Main", "Lyrics", "Library"} {
		m = send(m, runes("l"))
		if got := m.FocusedPanel(); got != want {
			t.Fatalf("after 'l' focus = %q, want %q", got, want)
		}
	}
	// The number keys never select the lyrics panel.
	for _, k := range []string{"1", "2", "3", "4"} {
		m = send(m, runes(k))
		if got := m.FocusedPanel(); got == "Lyrics" {
			t.Fatalf("number key %q selected the lyrics panel", k)
		}
	}
}

// TestLyricsUnsyncedScroll: a focused, visible unsynced-lyrics panel scrolls a
// plain offset with j (one line) and ctrl+d (half page), and the rendered first
// visible line follows the offset.
func TestLyricsUnsyncedScroll(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", unsyncedResult(60))
	m = focusLyricsPanel(t, m)

	m = send(m, runes("j"))
	if got := m.LyricsScrollOffset(); got != 1 {
		t.Fatalf("after 'j' lyrics scroll = %d, want 1", got)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "PLAIN 01") || strings.Contains(v, "PLAIN 00") {
		t.Errorf("after scrolling one line the panel should start at \"PLAIN 01\" (no \"PLAIN 00\"):\n%s", v)
	}

	before := m.LyricsScrollOffset()
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if got := m.LyricsScrollOffset(); got != before+m.halfPageRows() {
		t.Fatalf("after ctrl+d lyrics scroll = %d, want %d", got, before+m.halfPageRows())
	}

	// g/G jump to the clamped edges: G lands at the bottom (offset > 0, with the
	// last lines still on screen), g returns to the top.
	m = send(m, runes("G"))
	if got := m.LyricsScrollOffset(); got <= 0 {
		t.Fatalf("after 'G' lyrics scroll should be at the bottom edge (>0), got %d", got)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "PLAIN 59") {
		t.Errorf("after 'G' the panel should show the last line \"PLAIN 59\":\n%s", v)
	}
	m = send(m, runes("g"))
	if got := m.LyricsScrollOffset(); got != 0 {
		t.Fatalf("after 'g' lyrics scroll = %d, want 0", got)
	}
}

// TestLyricsUnsyncedScrollReclampedOnResize: 'G' at a short height pins the
// unsynced offset to that height's max; a resize that GROWS the lyrics band
// shrinks the max, so the stale offset must be re-clamped immediately (not
// left under-filling the panel until the next scroll key).
func TestLyricsUnsyncedScrollReclampedOnResize(t *testing.T) {
	m := newTestModel(t, 120, 25) // band 9 => 7 content rows
	m = playingWith(m, "v1", unsyncedResult(60))
	m = focusLyricsPanel(t, m)

	m = send(m, runes("G"))
	small := m.LyricsScrollOffset()
	if small <= 0 {
		t.Fatalf("after 'G' scroll = %d, want bottom edge (>0)", small)
	}

	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40}) // band grows to 10
	r, _ := m.currentLyric()
	want := m.lyricsMaxScroll(r.ly.Plain)
	if got := m.LyricsScrollOffset(); got != want {
		t.Fatalf("after growing resize scroll = %d, want re-clamped max %d (was %d)", got, want, small)
	}
	if got := m.LyricsScrollOffset(); got >= small {
		t.Fatalf("scroll = %d, want < %d (the larger band must shrink the max)", got, small)
	}
	// The bottom line is still on screen — no blank rows below hidden content.
	if v := ansi.Strip(m.View()); !strings.Contains(v, "PLAIN 59") {
		t.Errorf("after resize the panel should still show the last line:\n%s", v)
	}
}

// TestLyricsHintsOmitScrollWhileUnscrollable: while the focused panel is loading
// or resolved with nothing, the scroll keys are no-ops, so the hint bar must not
// advertise them.
func TestLyricsHintsOmitScrollWhileUnscrollable(t *testing.T) {
	for name, r := range map[string]lyricResult{
		"loading":  {status: lyricLoading},
		"notFound": {status: lyricResolved, found: false},
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(t, 120, 40)
			m = playingWith(m, "v1", r)
			m = focusLyricsPanel(t, m)
			for _, h := range m.contextHints() {
				if h.key == "j/k" || h.key == "ctrl+u/d" || h.key == "g/G" {
					t.Errorf("%s lyrics hints advertise %q, which is a no-op", name, h.key)
				}
			}
		})
	}
}

// TestLyricsSyncedPeekDetachAndResume: a focused synced panel peek-scrolls,
// detaching auto-follow so the rendered line stops tracking the position; a later
// EvTimePos past the ~5s resume deadline re-engages follow and the rendered line
// tracks the live position again.
func TestLyricsSyncedPeekDetachAndResume(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", syncedResult(40)) // line i at 2*i seconds
	m = focusLyricsPanel(t, m)

	m.timePos = 10 // live line 5 (At = 10s)
	m = send(m, runes("j"))
	if !m.LyricsDetached() {
		t.Fatal("peek-scroll did not detach synced auto-follow")
	}
	// Detached: the panel shows the peeked line (6), not the live one, even as
	// playback advances.
	m = send(m, playerEventMsg(player.Event{Kind: player.EvTimePos, Float: 11})) // before deadline (15)
	if !m.LyricsDetached() {
		t.Fatal("auto-follow re-engaged before the resume deadline")
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "line 06") {
		t.Errorf("detached panel should show the peeked line \"line 06\":\n%s", v)
	}
	if strings.Contains(v, "line 30") {
		t.Errorf("detached panel must not jump to a far live line")
	}

	// A later EvTimePos past the deadline re-engages auto-follow and snaps to the
	// live line.
	m = send(m, playerEventMsg(player.Event{Kind: player.EvTimePos, Float: 16})) // past deadline
	if m.LyricsDetached() {
		t.Fatal("auto-follow did not re-engage after the resume deadline")
	}
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "line 08") { // 16s => line 8 (At = 16s)
		t.Errorf("after re-engaging, the panel should track the live line \"line 08\":\n%s", v)
	}
}

// TestLyricsEscReengagesWithoutPoppingStack: esc while the focused synced panel
// is detached snaps back to following and does NOT pop the main-view stack.
func TestLyricsEscReengagesWithoutPoppingStack(t *testing.T) {
	m := newTestModel(t, 120, 40)
	// Two frames so a stray pop would be observable.
	m.stack = append(m.stack, mainContent{title: "Second", tracks: mockLibraryTracks("Liked Songs")})
	m = playingWith(m, "v1", syncedResult(40))
	m = focusLyricsPanel(t, m)

	m.timePos = 10
	m = send(m, runes("j"))
	if !m.LyricsDetached() {
		t.Fatal("peek-scroll did not detach auto-follow")
	}
	depth := len(m.stack)
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.LyricsDetached() {
		t.Error("esc should re-engage auto-follow (clear detached)")
	}
	if len(m.stack) != depth {
		t.Errorf("esc while detached popped the stack: depth %d -> %d", depth, len(m.stack))
	}
}

// TestLyricsFocusFallsBackWhenHidden: when the lyrics panel is focused and then
// hides (the terminal becomes too short), focus falls back to the main view.
func TestLyricsFocusFallsBackWhenHidden(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = playingWith(m, "v1", syncedResult(10))
	m = focusLyricsPanel(t, m)

	// A resize too short to fit the lyrics band hides it.
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 20})
	if got := m.FocusedPanel(); got != "Main" {
		t.Fatalf("focus after the panel hid = %q, want Main", got)
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

// TestLyricsLateResultCachedAfterSkip: a lyrics result that lands AFTER the
// user skipped to another track is still recorded under its own videoID
// (the cache is keyed, only the current track's entry is displayed, so a late
// result is always valid for its key). Returning to the track must show the
// lyrics — never stick on "searching for lyrics…" — and must not refetch.
func TestLyricsLateResultCachedAfterSkip(t *testing.T) {
	stubbed := false
	orig := lyricsFetch
	lyricsFetch = func(_ context.Context, _ *ytm.Client, _ model.Track) (lyrics.Lyrics, bool) {
		stubbed = true
		return lyrics.Lyrics{}, false
	}
	defer func() { lyricsFetch = orig }()

	m := newTestModel(t, 120, 40)

	// Play track A: ensureLyrics seeds the loading entry and dispatches a fetch.
	m.hasNow = true
	m.nowPlaying = model.Track{VideoID: "a", Title: "Track A"}
	if cmd := m.ensureLyrics(); cmd == nil {
		t.Fatal("ensureLyrics dispatched no fetch for track A")
	}

	// Skip to track B before A's result lands.
	m.nowPlaying = model.Track{VideoID: "b", Title: "Track B"}
	_ = m.ensureLyrics()

	// A's late result arrives while B is playing: it must be recorded.
	m = send(m, lyricsMsg{videoID: "a", found: true,
		ly: lyrics.Lyrics{Plain: "A LYRICS", Source: lyrics.SourceYTMusic}})
	if r, ok := m.lyricsCache["a"]; !ok || r.status != lyricResolved {
		t.Fatalf("late result for a moved-past track should be cached; got %+v, ok=%v", r, ok)
	}

	// Return to track A: cache hit (no refetch), lyrics render — not "searching".
	m.nowPlaying = model.Track{VideoID: "a", Title: "Track A"}
	stubbed = false
	if cmd := m.ensureLyrics(); cmd != nil {
		cmd()
		if stubbed {
			t.Errorf("returning to a cached track must not refetch")
		}
	}
	v := ansi.Strip(m.View())
	if strings.Contains(v, "searching for lyrics…") {
		t.Errorf("track A stuck on the loading notice after its late result:\n%s", v)
	}
	if !strings.Contains(v, "A LYRICS") {
		t.Errorf("track A's cached lyrics should render:\n%s", v)
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
