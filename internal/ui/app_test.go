package ui

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/core"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// newTestModel builds a model in degraded mode (no player, no client) at the
// given size. It touches neither the real HOME nor the network.
func newTestModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := New(config.Default(), theme.Default(), nil, nil, core.NewQueue())
	return send(m, tea.WindowSizeMsg{Width: w, Height: h})
}

// send dispatches one message and returns the updated model.
func send(m Model, msg tea.Msg) Model {
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// runes builds a KeyMsg for the given printable key (e.g. "1", "T", "?").
func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestViewShowsPanelsAndMockTitle(t *testing.T) {
	m := newTestModel(t, 120, 40)
	v := m.View()
	for _, want := range []string{"1 Library", "2 Playlists", "3 Queue", "Liked Songs", "Never Gonna Give You Up"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() missing %q", want)
		}
	}
}

func TestFocusMovement(t *testing.T) {
	m := newTestModel(t, 120, 40)
	if got := m.FocusedPanel(); got != "Library" {
		t.Fatalf("initial focus = %q, want Library", got)
	}

	m = send(m, runes("2"))
	if got := m.FocusedPanel(); got != "Playlists" {
		t.Errorf("after '2' focus = %q, want Playlists", got)
	}

	m = send(m, runes("3"))
	if got := m.FocusedPanel(); got != "Queue" {
		t.Errorf("after '3' focus = %q, want Queue", got)
	}

	m = send(m, runes("4"))
	if got := m.FocusedPanel(); got != "Main" {
		t.Errorf("after '4' focus = %q, want Main", got)
	}

	m = send(m, runes("1"))
	if got := m.FocusedPanel(); got != "Library" {
		t.Errorf("after '1' focus = %q, want Library", got)
	}
}

func TestFocusCycleHL(t *testing.T) {
	m := newTestModel(t, 120, 40)
	// l cycles forward through 1→2→3→4 and wraps back to 1.
	for _, want := range []string{"Playlists", "Queue", "Main", "Library"} {
		m = send(m, runes("l"))
		if got := m.FocusedPanel(); got != want {
			t.Fatalf("after 'l' focus = %q, want %q", got, want)
		}
	}
	// h cycles backward and wraps from Library to Main.
	for _, want := range []string{"Main", "Queue", "Playlists", "Library"} {
		m = send(m, runes("h"))
		if got := m.FocusedPanel(); got != want {
			t.Fatalf("after 'h' focus = %q, want %q", got, want)
		}
	}
}

func TestCursorMovesWithJK(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, runes("4")) // focus main view (mock Liked Songs list)
	if got := m.FocusedPanel(); got != "Main" {
		t.Fatalf("focus = %q, want Main", got)
	}
	top := func() int { return m.stack[len(m.stack)-1].cursor }
	start := top()
	m = send(m, runes("j"))
	if got := top(); got != start+1 {
		t.Errorf("after 'j' cursor = %d, want %d", got, start+1)
	}
	m = send(m, runes("k"))
	if got := top(); got != start {
		t.Errorf("after 'k' cursor = %d, want %d", got, start)
	}

	// Arrows are volume keys now and must not move the list cursor.
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := top(); got != start {
		t.Errorf("after down-arrow cursor = %d, want %d (arrows control volume)", got, start)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := top(); got != start {
		t.Errorf("after up-arrow cursor = %d, want %d (arrows control volume)", got, start)
	}
}

func TestArtOptionsFollowPaletteMode(t *testing.T) {
	m := newTestModel(t, 120, 40)

	// Default ("auto"): covers keep their own colors via median-cut, and the
	// cache key is theme-independent.
	if o := m.artOptions(); o.Palette != nil || o.PaletteSize <= 0 {
		t.Errorf("auto art options = %+v, want PaletteSize>0 and no fixed palette", o)
	}
	if k := m.artKey("vid"); strings.Contains(k, m.th.Name) {
		t.Errorf("auto artKey %q must not depend on the theme", k)
	}

	// "theme": covers snap to the theme palette and re-render per theme.
	m.cfg.ArtPalette = config.ArtPaletteTheme
	if o := m.artOptions(); len(o.Palette) == 0 {
		t.Errorf("theme art options = %+v, want the theme palette", m.artOptions())
	}
	if k := m.artKey("vid"); !strings.Contains(k, m.th.Name) {
		t.Errorf("theme artKey %q must include the theme name", k)
	}
}

func TestHelpOverlayOpensAndCloses(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, runes("?"))
	v := m.View()
	// The help overlay lists every binding's help text.
	if !strings.Contains(v, "search") {
		t.Errorf("help overlay missing a known hint (\"search\"); got:\n%s", v)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayNone {
		t.Errorf("esc did not close help overlay; overlay = %v", m.overlay)
	}
}

// TestHelpOverlayDoesNotBlankBackground is the regression test for the overlay
// compositor: opening the help overlay must splice the modal box over the view
// without blanking the rows it sits on. Before the fix, whole rows under the box
// were overwritten with spaces; now the background survives on BOTH sides.
func TestHelpOverlayDoesNotBlankBackground(t *testing.T) {
	m := newTestModel(t, 120, 40)
	base := strings.Split(ansi.Strip(m.View()), "\n")
	mo := send(m, runes("?"))
	over := strings.Split(ansi.Strip(mo.View()), "\n")

	if len(base) != len(over) {
		t.Fatalf("overlay changed row count: base %d, overlay %d", len(base), len(over))
	}
	// The left-column header must still be present somewhere in the overlay view.
	if !strings.Contains(strings.Join(over, "\n"), "1 Library") {
		t.Fatalf("help overlay blanked the left column (\"1 Library\" gone)")
	}
	// Column 0 is the outer frame border on every framed row. The bug blanked it
	// to a space on the modal's rows; assert it survived verbatim on every row.
	for i := range base {
		br, or := []rune(base[i]), []rune(over[i])
		if len(br) == 0 || len(or) == 0 {
			continue
		}
		if br[0] != or[0] {
			t.Errorf("row %d column 0 changed under overlay: base %q overlay %q", i, base[i], over[i])
		}
	}
	// On a row the modal actually covers, the background to the LEFT of the box
	// must be preserved (the panel content "left of the box is still visible in
	// the same row as overlay content"). The longest common prefix between the
	// base and overlay rows is exactly that preserved left segment; the bug
	// reduced it to nothing.
	var diffRows []int
	for i := range base {
		if base[i] != over[i] {
			diffRows = append(diffRows, i)
		}
	}
	if len(diffRows) == 0 {
		t.Fatal("opening the overlay produced no visible change")
	}
	r := diffRows[len(diffRows)/2] // a row through the middle of the modal
	br, or := []rune(base[r]), []rune(over[r])
	lcp := 0
	for lcp < len(br) && lcp < len(or) && br[lcp] == or[lcp] {
		lcp++
	}
	if strings.TrimSpace(string(br[:lcp])) == "" {
		t.Fatalf("row %d: overlay blanked the background left of the box\nbase: %q\nover: %q",
			r, base[r], over[r])
	}
	if !strings.Contains(string(br[:lcp]), "│") {
		t.Errorf("row %d: preserved left segment has no left-column border: %q", r, string(br[:lcp]))
	}
}

func TestThemePickerOpens(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, runes("T"))
	if m.overlay != overlayTheme {
		t.Fatalf("'T' did not open theme overlay; overlay = %v", m.overlay)
	}
	if !strings.Contains(m.View(), "Themes") {
		t.Errorf("theme overlay View missing \"Themes\"")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayNone {
		t.Errorf("esc did not close theme overlay")
	}
}

func TestSearchOverlayOpens(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, runes("/"))
	if m.overlay != overlaySearch {
		t.Fatalf("'/' did not open search overlay; overlay = %v", m.overlay)
	}
	if !strings.Contains(m.View(), "Search") {
		t.Errorf("search overlay View missing \"Search\"")
	}
}

func TestTooSmallNotice(t *testing.T) {
	m := newTestModel(t, 50, 10)
	v := m.View()
	if !strings.Contains(v, "too small") {
		t.Errorf("small terminal did not show too-small notice; got:\n%s", v)
	}
}

func TestEnterOnMainWithNilPlayerSetsStatus(t *testing.T) {
	m := newTestModel(t, 120, 40)
	// Move focus to the main view, then activate the selected track.
	m = send(m, runes("4"))
	if got := m.FocusedPanel(); got != "Main" {
		t.Fatalf("after '4' focus = %q, want Main", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.status == "" {
		t.Errorf("enter on main with nil player did not set a status message")
	}
	if !strings.Contains(m.View(), "mpv not found") {
		t.Errorf("status notice not shown in View; status = %q", m.status)
	}
	// The queue should have been populated even though playback is disabled.
	if m.q.Len() == 0 {
		t.Errorf("expected queue to be populated by main-view enter")
	}
}

func TestEscPopsMainStack(t *testing.T) {
	m := newTestModel(t, 120, 40)
	// Push a second main-view frame and confirm esc pops it.
	m.pushMain("Search: \"x\"", mockTracks)
	if len(m.stack) != 2 {
		t.Fatalf("expected 2 stack frames, got %d", len(m.stack))
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.stack) != 1 {
		t.Errorf("esc did not pop main stack; len = %d", len(m.stack))
	}
}

func TestLayoutDimensions(t *testing.T) {
	const w, h = 120, 40
	m := newTestModel(t, w, h)
	lines := strings.Split(m.View(), "\n")
	if len(lines) != h {
		t.Fatalf("View() produced %d lines, want %d", len(lines), h)
	}
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got != w {
			t.Errorf("line %d width = %d, want %d", i, got, w)
		}
	}

	// Overlay composite must preserve overall dimensions.
	mo := send(m, runes("?"))
	olines := strings.Split(mo.View(), "\n")
	if len(olines) != h {
		t.Errorf("overlay View() produced %d lines, want %d", len(olines), h)
	}
	for i, ln := range olines {
		if got := lipgloss.Width(ln); got != w {
			t.Errorf("overlay line %d width = %d, want %d", i, got, w)
		}
	}
}

func TestQuit(t *testing.T) {
	m := newTestModel(t, 120, 40)
	_, cmd := m.Update(runes("q"))
	if cmd == nil {
		t.Fatalf("q produced no command")
	}
	if msg := cmd(); msg == nil {
		t.Errorf("q command did not produce a quit message")
	}
}

// TestLogoVisibleAtLargeTerminal asserts that the ASCII logo wordmark is present
// in the rendered view when the terminal is tall enough (>= logoMinTermHeight).
func TestLogoVisibleAtLargeTerminal(t *testing.T) {
	m := newTestModel(t, 120, 40)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "tubeamp") {
		t.Errorf("View(120×40) should show the logo wordmark; \"tubeamp\" not found in:\n%s", v)
	}
}

// TestLogoWordmarkAndIndicator asserts the 3-row cyberpunk logo renders the
// "tubeamp" wordmark, the ▶ play glyph, the right-aligned version, AND that the
// sign-in indicator still rides the logo's rule row alongside the taller logo.
func TestLogoWordmarkAndIndicator(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	v := ansi.Strip(m.View())
	for _, want := range []string{"tubeamp", "▶", appVersion, "● Felipe Kallas"} {
		if !strings.Contains(v, want) {
			t.Errorf("logo view missing %q in:\n%s", want, v)
		}
	}
}

// TestLogoHiddenAtSmallTerminal asserts that the logo is suppressed when the
// terminal height is below logoMinTermHeight — the wordmark must not appear.
func TestLogoHiddenAtSmallTerminal(t *testing.T) {
	m := newTestModel(t, 120, 22)
	v := ansi.Strip(m.View())
	if strings.Contains(v, "tubeamp") {
		t.Errorf("View(120×22) should hide the logo; \"tubeamp\" unexpectedly found")
	}
}

// TestAlbumColumnVisibleAtWideWidth asserts the main-view track table shows the
// ALBUM column when the main view is wide enough (terminal 120 → main view ~84
// cols). "Nevermind" is a mock album short enough to fit the album column.
func TestAlbumColumnVisibleAtWideWidth(t *testing.T) {
	m := newTestModel(t, 120, 40)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Nevermind") {
		t.Errorf("View(120×40) should show the album column; \"Nevermind\" not found in:\n%s", v)
	}
}

// TestAlbumColumnHiddenAtNarrowWidth asserts the ALBUM column is dropped when the
// main view is narrower than ~80 cols (terminal 80 → main view ~56 cols), while
// the track row itself (its title) is still listed.
func TestAlbumColumnHiddenAtNarrowWidth(t *testing.T) {
	m := newTestModel(t, 80, 24)
	v := ansi.Strip(m.View())
	if strings.Contains(v, "Nevermind") {
		t.Errorf("View(80×24) should hide the album column; \"Nevermind\" unexpectedly present")
	}
	if !strings.Contains(v, "Smells Like Teen Spirit") {
		t.Errorf("View(80×24) dropped the track row; \"Smells Like Teen Spirit\" missing")
	}
}

// fakeAlbum returns a deterministic album + tracks for injecting into the model.
func fakeAlbum() (model.Album, []model.Track) {
	a := model.Album{
		BrowseID: "MPREb_fakeAlbum",
		Title:    "Test Album XYZ",
		Artists:  []string{"Fake Artist"},
		Year:     "2021",
	}
	ts := []model.Track{
		{VideoID: "fakevid1", Title: "Test Track One", Artists: []string{"Fake Artist"}, Album: "Test Album XYZ", Duration: 3 * time.Minute},
		{VideoID: "fakevid2", Title: "Test Track Two", Artists: []string{"Fake Artist"}, Album: "Test Album XYZ", Duration: 4 * time.Minute},
	}
	return a, ts
}

// TestAlbumViewShowsHeaderTracksAndPlaceholderArt injects an album view frame and
// asserts the rendered view shows the album title, year, a track row, and (since
// no cover has loaded) the placeholder pixel-art cover.
func TestAlbumViewShowsHeaderTracksAndPlaceholderArt(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()
	m.pushAlbum(a, ts)
	m.setFocus(focusMain)

	v := ansi.Strip(m.View())
	for _, want := range []string{"Test Album XYZ", "2021", "Test Track One"} {
		if !strings.Contains(v, want) {
			t.Errorf("album view missing %q in:\n%s", want, v)
		}
	}
	// art.Placeholder/Render emit the UPPER HALF BLOCK; the player bar art is
	// blank (nothing playing), so the only ▀ comes from the album cover.
	if !strings.Contains(v, "▀") {
		t.Errorf("album view missing the placeholder cover art (no ▀ half-block)")
	}
}

// TestAlbumViewEnterWithNilPlayerToasts confirms enter in the album view (play
// from the selected track) does not panic when the player is nil: it sets the
// disabled notice and still populates the queue with the whole album.
func TestAlbumViewEnterWithNilPlayerToasts(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()
	m.pushAlbum(a, ts)
	m.setFocus(focusMain)

	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.status == "" {
		t.Errorf("enter in album view with nil player set no status")
	}
	if !strings.Contains(m.View(), "mpv not found") {
		t.Errorf("album-view enter notice not shown; status = %q", m.status)
	}
	if m.q.Len() != len(ts) {
		t.Errorf("album-view enter should queue all %d tracks, got %d", len(ts), m.q.Len())
	}
}

// TestLateSearchResultUpdatesBuriedSearchFrame covers songs and albums arriving
// as separate messages: when the user has already opened an album view from the
// section that arrived first, the late result must update the original search
// frame in place — never push a duplicate frame over the album view or steal
// the top of the stack.
func TestLateSearchResultUpdatesBuriedSearchFrame(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()

	m.searchGen = 7
	m = send(m, albumSearchMsg{gen: 7, query: "x", albums: []model.Album{a}}) // search frame pushed
	m.pushAlbum(a, ts)                                                        // user opened the album ('o' flow)
	if len(m.stack) != 3 {
		t.Fatalf("setup: stack depth = %d, want 3", len(m.stack))
	}

	m = send(m, searchResultMsg{gen: 7, query: "x", tracks: ts}) // late songs result
	if len(m.stack) != 3 {
		t.Fatalf("late songs result changed stack depth to %d, want 3 (no duplicate search frame)", len(m.stack))
	}
	if top := m.stack[len(m.stack)-1]; top.kind != mainAlbum {
		t.Errorf("top frame kind = %v, want the album view to stay on top", top.kind)
	}
	if got := len(m.stack[1].tracks); got != len(ts) {
		t.Errorf("buried search frame got %d tracks, want %d", got, len(ts))
	}
}

// TestEscInvalidatesPendingAlbumLoad asserts navigating back drops an in-flight
// GetAlbum: a late result must neither push an album view over the new context
// nor silently replace the playback queue.
func TestEscInvalidatesPendingAlbumLoad(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()

	m.searchGen = 1
	m = send(m, albumSearchMsg{gen: 1, query: "x", albums: []model.Album{a}})
	m.albumGen = 3 // a GetAlbum (enter/'o' on the album row) is in flight

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc}) // back to the previous frame
	depth := len(m.stack)

	m = send(m, albumLoadMsg{gen: 3, open: true, album: a, tracks: ts})
	if len(m.stack) != depth {
		t.Errorf("stale album load pushed a view: depth %d -> %d", depth, len(m.stack))
	}
	m = send(m, albumLoadMsg{gen: 3, open: false, album: a, tracks: ts})
	if m.q.Len() != 0 {
		t.Errorf("stale album load replaced the queue with %d tracks", m.q.Len())
	}
}

// TestLateAlbumCoverIsCached pins the album-cover policy: covers are
// content-addressed by browseID, so an arriving download is always cached
// (decoded + rendered for the current theme) — even when it lands "late", e.g.
// for a view that was closed and re-opened while the deduped fetch was still in
// flight. Before the fix a generation guard discarded such covers entirely,
// leaving the open view stuck on the placeholder.
func TestLateAlbumCoverIsCached(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()
	m.pushAlbum(a, ts)

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	m = send(m, albumCoverMsg{browseID: a.BrowseID, img: img})

	if _, ok := m.albumImgCache[a.BrowseID]; !ok {
		t.Error("decoded cover not cached by browseID")
	}
	if _, ok := m.albumArtCache[m.artKey(a.BrowseID)]; !ok {
		t.Error("rendered cover not cached under the current palette mode key")
	}
}

// TestLogoVisibilityBoundary pins the logo's height boundary: visible at
// exactly logoMinTermHeight, hidden one row below — and in both modes the
// rendered view still totals exactly the terminal height with full-width rows
// (the logoHeight logo rows are reclaimed by the panel area when hidden).
func TestLogoVisibilityBoundary(t *testing.T) {
	for _, tc := range []struct {
		h       int
		visible bool
	}{
		{logoMinTermHeight, true},      // first height that shows the logo
		{logoMinTermHeight - 1, false}, // last height that hides it
	} {
		m := newTestModel(t, 120, tc.h)
		raw := m.View()
		if got := strings.Contains(ansi.Strip(raw), "tubeamp"); got != tc.visible {
			t.Errorf("h=%d: logo visible = %v, want %v", tc.h, got, tc.visible)
		}
		lines := strings.Split(raw, "\n")
		if len(lines) != tc.h {
			t.Errorf("h=%d: view has %d rows, want exactly %d", tc.h, len(lines), tc.h)
		}
		for i, ln := range lines {
			if got := lipgloss.Width(ln); got != 120 {
				t.Errorf("h=%d: line %d width = %d, want 120", tc.h, i, got)
				break
			}
		}
	}
}

// TestLayoutInvariantAcrossHeights sweeps heights spanning the (taller, 3-row)
// logo boundary and asserts the rendered view always totals exactly the terminal
// height with full-width rows — i.e. the 3-row logo never makes the panel area
// overflow or clip at the minimum sizes.
func TestLayoutInvariantAcrossHeights(t *testing.T) {
	for _, w := range []int{minWidth, 120} {
		for h := minHeight; h <= logoMinTermHeight+3; h++ {
			m := newTestModel(t, w, h)
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

// TestAuthIndicatorBeforeCheck asserts no sign-in indicator is shown until the
// one-shot AccountInfo check resolves.
func TestAuthIndicatorBeforeCheck(t *testing.T) {
	m := newTestModel(t, 120, 40)
	if got := m.authIndicator(); got != "" {
		t.Errorf("indicator before check = %q, want empty", got)
	}
}

// TestAuthIndicatorSignedIn injects a signed-in AccountInfo result (via the msg,
// not the network) and asserts the name shows with the filled bullet in the view.
func TestAuthIndicatorSignedIn(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	if !m.authChecked || !m.authSignedIn {
		t.Fatalf("signed-in msg not applied: checked=%v signedIn=%v", m.authChecked, m.authSignedIn)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "● Felipe Kallas") {
		t.Errorf("view missing signed-in indicator \"● Felipe Kallas\" in:\n%s", v)
	}
}

// TestAuthIndicatorAnonymousWithAuth covers the stale-cookie case: an auth file
// is present (hasAuth) but YouTube returns the logged-out menu.
func TestAuthIndicatorAnonymousWithAuth(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m.hasAuth = true // an auth file was loaded
	m = send(m, accountInfoMsg{signedIn: false})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "anonymous") || !strings.Contains(v, "cookie stale") {
		t.Errorf("view missing stale-cookie indicator in:\n%s", v)
	}
}

// TestAuthIndicatorNotSignedIn covers the no-auth-file case: anonymous result and
// no cookie loaded yields "not signed in".
func TestAuthIndicatorNotSignedIn(t *testing.T) {
	m := newTestModel(t, 120, 40) // nil client => hasAuth false
	m = send(m, accountInfoMsg{signedIn: false})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "not signed in") {
		t.Errorf("view missing \"not signed in\" indicator in:\n%s", v)
	}
}

// TestAuthIndicatorErrorStaysUnresolved asserts a failed check (network down)
// leaves the indicator blank rather than flashing a spurious state.
func TestAuthIndicatorErrorStaysUnresolved(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, accountInfoMsg{err: context.DeadlineExceeded})
	if m.authChecked {
		t.Errorf("authChecked set despite error")
	}
	if got := m.authIndicator(); got != "" {
		t.Errorf("indicator after error = %q, want empty", got)
	}
}

// TestAuthIndicatorInStatusRowWhenLogoHidden asserts that with the logo hidden
// (short terminal) the indicator falls through to the bottom status line.
func TestAuthIndicatorInStatusRowWhenLogoHidden(t *testing.T) {
	m := newTestModel(t, 120, 22) // below logoMinTermHeight => logo hidden
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	v := ansi.Strip(m.View())
	if strings.Contains(v, "tubeamp") {
		t.Fatalf("logo should be hidden at height 22")
	}
	if !strings.Contains(v, "● Felipe Kallas") {
		t.Errorf("indicator not shown in status row when logo hidden:\n%s", v)
	}
}

// TestLibraryPlaylistsMsgFillsPanel injects a real LibraryPlaylists result and
// asserts it replaces the mock playlists in the panel.
func TestLibraryPlaylistsMsgFillsPanel(t *testing.T) {
	m := newTestModel(t, 120, 40)
	// The mock playlists are present before the real fill.
	if !strings.Contains(ansi.Strip(m.View()), "Focus Deep Work") {
		t.Fatalf("setup: mock playlist \"Focus Deep Work\" not shown")
	}

	real := []model.Playlist{
		{ID: "PLreal1", Title: "My Real Playlist", TrackCount: 12},
		{ID: "PLreal2", Title: "Another Real One", TrackCount: 5},
	}
	m.plGen = 1
	m = send(m, libPlaylistsMsg{gen: 1, playlists: real})

	if len(m.playlists) != 2 || m.playlists[0].Title != "My Real Playlist" {
		t.Fatalf("playlists not replaced: %+v", m.playlists)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "My Real Playlist") {
		t.Errorf("Playlists panel missing real playlist title in:\n%s", v)
	}
	if strings.Contains(v, "Focus Deep Work") {
		t.Errorf("Playlists panel still shows a mock playlist after the real fill")
	}
}

// TestLibraryPlaylistsMsgStaleIgnored asserts a result tagged with an outdated
// generation is dropped (the panel keeps its current contents).
func TestLibraryPlaylistsMsgStaleIgnored(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m.plGen = 2
	before := len(m.playlists)
	m = send(m, libPlaylistsMsg{gen: 1, playlists: []model.Playlist{{ID: "x", Title: "Stale"}}})
	if len(m.playlists) != before {
		t.Errorf("stale libPlaylistsMsg changed the panel: %d -> %d", before, len(m.playlists))
	}
}

// TestLibraryPlaylistsMsgNotSignedInKeepsMock asserts an anonymous-session result
// keeps the mock playlists and toasts the sign-in hint.
func TestLibraryPlaylistsMsgNotSignedInKeepsMock(t *testing.T) {
	m := newTestModel(t, 120, 40)
	before := append([]model.Playlist(nil), m.playlists...)
	m.plGen = 1
	m = send(m, libPlaylistsMsg{gen: 1, err: ytm.ErrNotSignedIn})

	if len(m.playlists) != len(before) {
		t.Errorf("ErrNotSignedIn replaced the mock playlists")
	}
	if !strings.Contains(m.status, "sign in to load your library") {
		t.Errorf("status = %q, want sign-in hint", m.status)
	}
}

// TestLikedSongsMsgLandsInMainView injects a real Liked Songs result and asserts
// the tracks become the main view, focused, titled "Liked Songs".
func TestLikedSongsMsgLandsInMainView(t *testing.T) {
	m := newTestModel(t, 120, 40)
	_, ts := fakeAlbum() // two tracks: "Test Track One"/"Test Track Two"

	m.libGen = 1
	m = send(m, libTracksMsg{gen: 1, title: "Liked Songs", tracks: ts})

	if m.FocusedPanel() != "Main" {
		t.Fatalf("Liked Songs result did not focus the main view; focus = %q", m.FocusedPanel())
	}
	top := m.stack[len(m.stack)-1]
	if top.title != "Liked Songs" || len(top.tracks) != len(ts) {
		t.Fatalf("main frame = {title:%q, tracks:%d}, want {Liked Songs, %d}", top.title, len(top.tracks), len(ts))
	}
	if !strings.Contains(ansi.Strip(m.View()), "Test Track One") {
		t.Errorf("main view missing the loaded liked song")
	}
}

// TestLikedSongsMsgNotSignedInTogglesToastAndKeepsMock asserts an anonymous
// result toasts the sign-in hint and leaves the existing (mock) main view intact.
func TestLikedSongsMsgNotSignedInTogglesToastAndKeepsMock(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m.libGen = 1
	m = send(m, libTracksMsg{gen: 1, title: "Liked Songs", err: ytm.ErrNotSignedIn})

	if !strings.Contains(m.status, "sign in to load your library") {
		t.Errorf("status = %q, want sign-in hint", m.status)
	}
	// The startup mock Liked Songs view is untouched.
	if !strings.Contains(ansi.Strip(m.View()), "Never Gonna Give You Up") {
		t.Errorf("ErrNotSignedIn replaced the mock main view")
	}
}

// TestLibTracksMsgStaleIgnored asserts a track result from a superseded load is
// dropped rather than stealing the main view.
func TestLibTracksMsgStaleIgnored(t *testing.T) {
	m := newTestModel(t, 120, 40)
	_, ts := fakeAlbum()
	m.libGen = 5
	m = send(m, libTracksMsg{gen: 4, title: "Liked Songs", tracks: ts})
	if strings.Contains(ansi.Strip(m.View()), "Test Track One") {
		t.Errorf("stale libTracksMsg stole the main view")
	}
}

// TestSearchResultsRenderBothSections delivers fake songs and album results (which
// arrive as separate messages) and asserts both sections render in the main view.
func TestSearchResultsRenderBothSections(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()

	m.searchGen = 7
	m = send(m, searchResultMsg{gen: 7, query: "test", tracks: ts})
	m = send(m, albumSearchMsg{gen: 7, query: "test", albums: []model.Album{a}})

	if m.FocusedPanel() != "Main" {
		t.Fatalf("search results did not focus the main view; focus = %q", m.FocusedPanel())
	}
	v := ansi.Strip(m.View())
	// "Songs"/"Albums" also appear in the library panel, so assert on distinctive
	// content: a song row, the album-row glyph, and the album result title.
	for _, want := range []string{"Test Track One", "▤", "Test Album XYZ"} {
		if !strings.Contains(v, want) {
			t.Errorf("search results view missing %q in:\n%s", want, v)
		}
	}
}

// TestDerivedAlbumsOrderedFirstAndDeduped pins the album-fallback merge: albums
// derived from the full-catalog song hits come first, then the album-vertical
// results, deduped by BrowseID — regardless of which response lands first.
func TestDerivedAlbumsOrderedFirstAndDeduped(t *testing.T) {
	m := newTestModel(t, 120, 40)
	ts := []model.Track{{VideoID: "v1", Title: "S1", Artists: []string{"A"}, Album: "Derived First", Duration: time.Minute}}
	derived := model.Album{BrowseID: "MPRE_derived", Title: "Derived First", Artists: []string{"A"}}
	dup := model.Album{BrowseID: "MPRE_derived", Title: "Derived First (vertical dup)", Artists: []string{"A"}}
	vertical := model.Album{BrowseID: "MPRE_vertical", Title: "Vertical Only", Artists: []string{"B"}, Year: "2000"}

	m.searchGen = 9
	// The albums vertical arrives FIRST (out of order)…
	m = send(m, albumSearchMsg{gen: 9, query: "q", albums: []model.Album{dup, vertical}})
	// …then the songs (carrying derived albums) arrive.
	m = send(m, searchResultMsg{gen: 9, query: "q", tracks: ts, derivedAlbums: []model.Album{derived}})

	top := m.stack[len(m.stack)-1]
	if top.kind != mainSearch {
		t.Fatalf("top frame kind = %v, want mainSearch", top.kind)
	}
	got := top.albums
	if len(got) != 2 {
		t.Fatalf("merged albums = %d, want 2 (deduped by BrowseID)", len(got))
	}
	if got[0].BrowseID != "MPRE_derived" {
		t.Errorf("albums[0].BrowseID = %q, want MPRE_derived first (derived-from-songs ordered first)", got[0].BrowseID)
	}
	if got[0].Title != "Derived First" {
		t.Errorf("albums[0].Title = %q, want the derived ref kept over the vertical dup", got[0].Title)
	}
	if got[1].BrowseID != "MPRE_vertical" {
		t.Errorf("albums[1].BrowseID = %q, want MPRE_vertical second", got[1].BrowseID)
	}
}

// TestGetAlbumFromDerivedAlbumWithEmptyYear verifies a derived album (empty Year)
// renders without an empty "()" segment and that opening it (GetAlbum) fills the
// year on the album view — the fallback must not regress the album flow.
func TestGetAlbumFromDerivedAlbumWithEmptyYear(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	derived := model.Album{BrowseID: "MPREb_derived", Title: "Derived Album", Artists: []string{"Some Artist"}, ThumbURL: "https://lh3.googleusercontent.com/x"}
	ts := []model.Track{{VideoID: "v1", Title: "Track A", Artists: []string{"Some Artist"}, Album: "Derived Album", Duration: 3 * time.Minute}}

	m.searchGen = 5
	m = send(m, searchResultMsg{gen: 5, query: "derived", tracks: ts, derivedAlbums: []model.Album{derived}})

	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Derived Album") {
		t.Fatalf("search view missing derived album title:\n%s", v)
	}
	if strings.Contains(v, "()") {
		t.Errorf("derived album with empty Year rendered an empty () segment:\n%s", v)
	}

	// Move the cursor onto the album row (one song, so the first album is index 1).
	m.stack[len(m.stack)-1].cursor = len(ts)
	cmd := m.handleOpen()
	if cmd == nil {
		t.Fatal("handleOpen returned nil cmd for a derived album row")
	}

	// Simulate the GetAlbum result filling in the year the derived ref lacked.
	full := model.Album{BrowseID: "MPREb_derived", Title: "Derived Album", Artists: []string{"Some Artist"}, Year: "1999"}
	m = send(m, albumLoadMsg{gen: m.albumGen, open: true, title: "Derived Album", album: full, tracks: ts})

	av := ansi.Strip(m.View())
	if !strings.Contains(av, "Derived Album") || !strings.Contains(av, "1999") {
		t.Errorf("album view missing title/year after GetAlbum:\n%s", av)
	}
}

// TestPlaylistEnterWhilePanelStillMockPlaysMock guards the sign-in/fill race:
// a signed-in session whose Playlists panel still holds mock data (the
// LibraryPlaylists fill is in flight or failed) must play the mock tracks
// locally — a mock ID like "focus" must never reach a real PlaylistTracks
// browse (browseId "VLfocus").
func TestPlaylistEnterWhilePanelStillMockPlaysMock(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	if !m.canLoadLibrary() {
		t.Fatal("setup: expected canLoadLibrary after signed-in result")
	}

	m = send(m, runes("2")) // focus Playlists (still mock: fill not landed)
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd != nil {
		t.Fatal("enter on a mock playlist row dispatched a command (would browse a fake ID)")
	}
	top := m.stack[len(m.stack)-1]
	if top.title != "Focus Deep Work" || len(top.tracks) == 0 {
		t.Errorf("mock playlist did not open its mock tracks: title=%q tracks=%d", top.title, len(top.tracks))
	}
}

// TestPlaylistEnterRealAfterPanelFilled asserts the real browse path engages
// only once the panel holds real playlists.
func TestPlaylistEnterRealAfterPanelFilled(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	m.plGen = 1
	m = send(m, libPlaylistsMsg{gen: 1, playlists: []model.Playlist{{ID: "PLreal1", Title: "My Real Playlist"}}})
	if !m.playlistsReal {
		t.Fatal("setup: playlistsReal not set after successful fill")
	}

	m = send(m, runes("2"))
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter on a real playlist row dispatched no PlaylistTracks command")
	}
	if !strings.Contains(m.status, "loading") {
		t.Errorf("status = %q, want a loading notice", m.status)
	}
}

// TestPlaylistEnterAnonymousToastsSignInHint pins the documented anonymous
// behavior: with a client present but the session resolved anonymous, opening a
// (mock) playlist loads the mock tracks AND toasts the sign-in hint.
func TestPlaylistEnterAnonymousToastsSignInHint(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = send(m, accountInfoMsg{signedIn: false})

	m = send(m, runes("2"))
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.status, "sign in to load your library") {
		t.Errorf("status = %q, want the sign-in hint", m.status)
	}
	top := m.stack[len(m.stack)-1]
	if top.title != "Focus Deep Work" || len(top.tracks) == 0 {
		t.Errorf("anonymous playlist enter did not open mock tracks: title=%q tracks=%d", top.title, len(top.tracks))
	}
}

// TestSearchInvalidatesPendingLibraryLoad covers the stack-push hole: a Liked
// Songs load in flight when the user issues a new search must be dropped — its
// late result may not setMain over (and so destroy) the search-results frame.
func TestSearchInvalidatesPendingLibraryLoad(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})

	// Enter on "Liked Songs" issues the real load (the Cmd is never executed).
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	pendingGen := m.libGen

	// The user issues a new search before the load lands…
	m = send(m, runes("/"))
	m = send(m, runes("rush"))
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.libGen == pendingGen {
		t.Fatal("issuing a search did not invalidate the in-flight library load")
	}

	// …its results land and the user is browsing them…
	_, ts := fakeAlbum()
	m = send(m, searchResultMsg{gen: m.searchGen, query: "rush", tracks: ts})
	if top := m.stack[len(m.stack)-1]; top.kind != mainSearch {
		t.Fatalf("setup: top frame kind = %v, want mainSearch", top.kind)
	}
	depth := len(m.stack)

	// …when the stale library result finally arrives: it must be dropped.
	m = send(m, libTracksMsg{gen: pendingGen, title: "Liked Songs", tracks: mockTracks})
	if top := m.stack[len(m.stack)-1]; top.kind != mainSearch || len(m.stack) != depth {
		t.Errorf("late library load stole the view: kind=%v depth=%d (want mainSearch, %d)", top.kind, len(m.stack), depth)
	}
}

// TestNotSignedInDowngradesAuth asserts a library browse coming back logged-out
// flips the resolved sign-in state to anonymous: the indicator switches to the
// stale-cookie hint and real library loads stop being issued, instead of an
// "● <name>" header forever contradicting "sign in to load your library" toasts.
func TestNotSignedInDowngradesAuth(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m.hasAuth = true // an auth file was loaded
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})

	m.libGen = 1
	m = send(m, libTracksMsg{gen: 1, title: "Liked Songs", err: ytm.ErrNotSignedIn})

	if m.authSignedIn || m.authName != "" {
		t.Errorf("auth not downgraded: signedIn=%v name=%q", m.authSignedIn, m.authName)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "anonymous") || !strings.Contains(v, "cookie stale") {
		t.Errorf("indicator did not switch to the stale-cookie hint:\n%s", v)
	}
	if strings.Contains(v, "● Felipe Kallas") {
		t.Errorf("signed-in indicator still shown after downgrade")
	}
}

// TestMergeEnrichesDerivedAlbumFromVertical pins the per-BrowseID metadata
// merge: an album present in both sources keeps its derived-first position and
// title but takes the vertical's richer Year, artists and cover — the derived
// ref's stand-ins (song artists incl. features, song thumb, empty Year) must
// not downgrade the rendered row.
func TestMergeEnrichesDerivedAlbumFromVertical(t *testing.T) {
	m := newTestModel(t, 120, 40)
	ts := []model.Track{{VideoID: "v1", Title: "S1", Artists: []string{"Album Artist", "Feature"}, Album: "Moving Pictures", Duration: time.Minute}}
	derived := model.Album{BrowseID: "MPRE_mp", Title: "Moving Pictures", Artists: []string{"Album Artist", "Feature"}, ThumbURL: "https://song-thumb"}
	vertical := model.Album{BrowseID: "MPRE_mp", Title: "Moving Pictures (vertical)", Artists: []string{"Album Artist"}, Year: "1981", ThumbURL: "https://album-cover"}

	m.searchGen = 3
	m = send(m, searchResultMsg{gen: 3, query: "q", tracks: ts, derivedAlbums: []model.Album{derived}})
	m = send(m, albumSearchMsg{gen: 3, query: "q", albums: []model.Album{vertical}})

	top := m.stack[len(m.stack)-1]
	if len(top.albums) != 1 {
		t.Fatalf("merged albums = %d, want 1", len(top.albums))
	}
	got := top.albums[0]
	if got.Title != "Moving Pictures" {
		t.Errorf("Title = %q, want the derived ref's title kept", got.Title)
	}
	if got.Year != "1981" {
		t.Errorf("Year = %q, want %q from the vertical record", got.Year, "1981")
	}
	if len(got.Artists) != 1 || got.Artists[0] != "Album Artist" {
		t.Errorf("Artists = %v, want the vertical's true album artists", got.Artists)
	}
	if got.ThumbURL != "https://album-cover" {
		t.Errorf("ThumbURL = %q, want the vertical's album cover", got.ThumbURL)
	}
}

// TestLateSongsPreserveAlbumSelection covers out-of-order arrival: the albums
// vertical lands first, the user highlights an album, then the songs result
// prepends the Songs section and reorders the Albums section (derived-first).
// The highlighted row must keep its identity — the same album, at its new
// combined index — rather than silently becoming an unrelated song.
func TestLateSongsPreserveAlbumSelection(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a1 := model.Album{BrowseID: "MPRE_a1", Title: "First Vertical"}
	a2 := model.Album{BrowseID: "MPRE_a2", Title: "Second Vertical"}
	d := model.Album{BrowseID: "MPRE_d", Title: "Derived"}
	ts := []model.Track{
		{VideoID: "v1", Title: "Song One", Duration: time.Minute},
		{VideoID: "v2", Title: "Song Two", Duration: time.Minute},
	}

	m.searchGen = 4
	m = send(m, albumSearchMsg{gen: 4, query: "q", albums: []model.Album{a1, a2}})
	m.stack[len(m.stack)-1].cursor = 1 // user highlights a2 (no songs yet)

	m = send(m, searchResultMsg{gen: 4, query: "q", tracks: ts, derivedAlbums: []model.Album{d}})

	top := m.stack[len(m.stack)-1]
	// Merged albums: derived first, then the vertical pair => a2 at index 2.
	wantCursor := len(ts) + 2
	if top.cursor != wantCursor {
		t.Fatalf("cursor = %d, want %d (selection follows album %q)", top.cursor, wantCursor, a2.Title)
	}
	if got := top.albums[top.cursor-len(top.tracks)].BrowseID; got != a2.BrowseID {
		t.Errorf("selected album = %q, want %q", got, a2.BrowseID)
	}
}

// TestLogoIndicatorTruncatedAtNarrowWidth asserts a long account name clips
// with an ellipsis on the logo rule row instead of the indicator vanishing
// entirely (its only home when the logo is visible).
func TestLogoIndicatorTruncatedAtNarrowWidth(t *testing.T) {
	m := newTestModel(t, 70, 40) // min width, logo visible
	m = send(m, accountInfoMsg{name: strings.Repeat("N", 60), signedIn: true})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "● N") {
		t.Errorf("long-name indicator dropped at narrow width:\n%s", v)
	}
	if !strings.Contains(v, "…") {
		t.Errorf("long-name indicator not truncated with an ellipsis:\n%s", v)
	}
	// Every row must still be exactly the terminal width.
	for i, ln := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(ln); got != 70 {
			t.Errorf("line %d width = %d, want 70", i, got)
			break
		}
	}
}

// ── Stale-session auto-refresh ───────────────────────────────────────────────

// staleModel builds a test model wired so the auto-refresh can fire: an auth file
// is present (hasAuth) and a browser is remembered to re-import from. reimportFn
// is replaced with fn so neither a browser nor the network is touched.
func staleModel(t *testing.T, browser string, fn reimportFunc) Model {
	t.Helper()
	m := newTestModel(t, 120, 40)
	m.hasAuth = true
	m.cfg.AuthBrowser = browser
	m.reimportFn = fn
	return m
}

// TestAutoRefresh_signedInStubFlipsIndicator: an anonymous startup check fires a
// one-shot re-import; a signed-in stub flips the indicator to signed-in and
// toasts "session refreshed from <browser>".
func TestAutoRefresh_signedInStubFlipsIndicator(t *testing.T) {
	var gotBrowser string
	m := staleModel(t, "chrome", func(browser string, _ int) reimportMsg {
		gotBrowser = browser
		return reimportMsg{browser: browser, client: ytm.NewClient(nil), name: "Felipe Kallas", signedIn: true}
	})

	// Anonymous startup result => a reimport Cmd is returned.
	updated, cmd := m.Update(accountInfoMsg{signedIn: false})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a reimport Cmd after an anonymous check")
	}

	// Run the Cmd (the injected stub) and feed its message back.
	m = send(m, cmd())
	if gotBrowser != "chrome" {
		t.Errorf("reimport called with browser %q, want chrome", gotBrowser)
	}
	if !m.authSignedIn || m.authName != "Felipe Kallas" {
		t.Fatalf("indicator did not flip to signed-in: signedIn=%v name=%q", m.authSignedIn, m.authName)
	}
	if !strings.Contains(m.status, "session refreshed from chrome") || m.statusErr {
		t.Errorf("status = %q (err=%v), want refreshed toast", m.status, m.statusErr)
	}
	if !strings.Contains(ansi.Strip(m.View()), "● Felipe Kallas") {
		t.Errorf("view missing refreshed signed-in indicator")
	}
}

// TestAutoRefresh_failingStubFallbackToast: a failing import stub leaves the
// session anonymous and toasts the run-the-command fallback hint.
func TestAutoRefresh_failingStubFallbackToast(t *testing.T) {
	m := staleModel(t, "firefox", func(browser string, _ int) reimportMsg {
		return reimportMsg{browser: browser, err: errors.New("no cookie store")}
	})

	updated, cmd := m.Update(accountInfoMsg{signedIn: false})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a reimport Cmd after an anonymous check")
	}
	m = send(m, cmd())

	if m.authSignedIn {
		t.Error("session should still be anonymous after a failed re-import")
	}
	if !strings.Contains(m.status, "re-import failed — run tubeamp -auth firefox") || !m.statusErr {
		t.Errorf("status = %q (err=%v), want fallback hint", m.status, m.statusErr)
	}
}

// TestAutoRefresh_firesAtMostOnce: a second anonymous result (e.g. a later
// library load that comes back logged out) must NOT fire another re-import.
func TestAutoRefresh_firesAtMostOnce(t *testing.T) {
	var calls int
	m := staleModel(t, "chrome", func(browser string, _ int) reimportMsg {
		calls++
		return reimportMsg{browser: browser, err: errors.New("denied")}
	})

	// First anonymous trigger fires the re-import.
	updated, cmd := m.Update(accountInfoMsg{signedIn: false})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("first anonymous check should fire a reimport Cmd")
	}
	m = send(m, cmd())

	// A later anonymous library load must not fire a second one.
	m.plGen = 1
	updated2, cmd2 := m.Update(libPlaylistsMsg{gen: 1, err: ytm.ErrNotSignedIn})
	m = updated2.(Model)
	if cmd2 != nil {
		t.Error("a second anonymous result should not fire another reimport")
	}
	if calls != 1 {
		t.Errorf("reimport called %d times, want exactly 1", calls)
	}
}

// TestAutoRefresh_skippedWithoutBrowser: with no remembered browser, an anonymous
// result fires no re-import (and reimportFn is never consulted).
func TestAutoRefresh_skippedWithoutBrowser(t *testing.T) {
	called := false
	m := newTestModel(t, 120, 40)
	m.hasAuth = true // auth file present, but no AuthBrowser remembered
	m.reimportFn = func(string, int) reimportMsg { called = true; return reimportMsg{} }

	_, cmd := m.Update(accountInfoMsg{signedIn: false})
	if cmd != nil {
		t.Error("no reimport Cmd should fire without a remembered browser")
	}
	if called {
		t.Error("reimportFn must not run without a remembered browser")
	}
}

// TestAutoRefresh_skippedWithoutAuthFile: a remembered browser but no auth file
// (a machine that never signed in) fires no re-import.
func TestAutoRefresh_skippedWithoutAuthFile(t *testing.T) {
	m := newTestModel(t, 120, 40) // nil client => hasAuth false
	m.cfg.AuthBrowser = "chrome"
	m.reimportFn = func(string, int) reimportMsg { return reimportMsg{} }

	_, cmd := m.Update(accountInfoMsg{signedIn: false})
	if cmd != nil {
		t.Error("no reimport Cmd should fire without an auth file present")
	}
}

// TestOpenAlbumFromSongRow: 'o' on a song row carrying an AlbumID fires a
// GetAlbum and, when the result arrives, pushes the album view.
func TestOpenAlbumFromSongRow(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// A plain track list (e.g. Liked Songs) whose song carries its album id.
	m.setMain("Liked Songs", []model.Track{
		{VideoID: "v1", Title: "Song One", Artists: []string{"Artist"}, Album: "Some Album", AlbumID: "MPREb_song1"},
	})
	m.setFocus(focusMain)

	mm, cmd := m.Update(runes("o"))
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("'o' on a song row with an AlbumID dispatched no GetAlbum command")
	}
	if !strings.Contains(m.status, "loading") {
		t.Errorf("status = %q, want a loading notice", m.status)
	}

	depth := len(m.stack)
	a, ts := fakeAlbum()
	m = send(m, albumLoadMsg{gen: m.albumGen, open: true, title: "Some Album", album: a, tracks: ts})
	if len(m.stack) != depth+1 {
		t.Fatalf("album view not pushed: depth %d -> %d", depth, len(m.stack))
	}
	if top := m.stack[len(m.stack)-1]; top.kind != mainAlbum {
		t.Errorf("top frame kind = %v, want mainAlbum after song-row open", top.kind)
	}
}

// TestOpenAlbumFromSongRowNoAlbumID: 'o' on a song with no AlbumID toasts and
// pushes nothing.
func TestOpenAlbumFromSongRowNoAlbumID(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m.setMain("Liked Songs", []model.Track{
		{VideoID: "v1", Title: "Song One", Artists: []string{"Artist"}}, // no AlbumID
	})
	m.setFocus(focusMain)
	depth := len(m.stack)

	mm, cmd := m.Update(runes("o"))
	m = mm.(Model)
	if cmd != nil {
		t.Error("'o' on a song with no AlbumID dispatched a command (would browse nothing)")
	}
	if !strings.Contains(m.status, "no album for this track") {
		t.Errorf("status = %q, want the no-album toast", m.status)
	}
	if len(m.stack) != depth {
		t.Errorf("stack depth changed to %d, want %d (no push)", len(m.stack), depth)
	}
}

// TestOpenAlbumFromQueueRow: 'o' on a queue row opens the song's album too.
func TestOpenAlbumFromQueueRow(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m.q.Set([]model.Track{
		{VideoID: "v1", Title: "Q1", Artists: []string{"A"}, Album: "Q Album", AlbumID: "MPREb_q1"},
	}, 0)
	m = send(m, runes("3")) // focus Queue
	m.queueCursor = 0

	mm, cmd := m.Update(runes("o"))
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("'o' on a queue row with an AlbumID dispatched no GetAlbum command")
	}

	depth := len(m.stack)
	a, ts := fakeAlbum()
	m = send(m, albumLoadMsg{gen: m.albumGen, open: true, album: a, tracks: ts})
	if len(m.stack) != depth+1 || m.stack[len(m.stack)-1].kind != mainAlbum {
		t.Errorf("queue 'o' did not push album view: depth %d -> %d", depth, len(m.stack))
	}
}

// TestOpenAlbumFromAlbumRowStillWorks: 'o' on a search-results album row keeps
// opening that album by its own browseId (unchanged behavior).
func TestOpenAlbumFromAlbumRowStillWorks(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	row := model.Album{BrowseID: "MPREb_row", Title: "Row Album", Artists: []string{"A"}}
	song := model.Track{VideoID: "v1", Title: "S1", Artists: []string{"A"}, Album: "Other", AlbumID: "MPREb_other"}
	m.searchGen = 4
	m = send(m, searchResultMsg{gen: 4, query: "q", tracks: []model.Track{song}, derivedAlbums: []model.Album{row}})

	// Move the cursor onto the album row (one song, album at index len(tracks)).
	m.stack[len(m.stack)-1].cursor = 1
	m.setFocus(focusMain)

	mm, cmd := m.Update(runes("o"))
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("'o' on an album row dispatched no GetAlbum command")
	}

	depth := len(m.stack)
	full, ts := fakeAlbum()
	full.BrowseID = "MPREb_row"
	m = send(m, albumLoadMsg{gen: m.albumGen, open: true, title: "Row Album", album: full, tracks: ts})
	if len(m.stack) != depth+1 || m.stack[len(m.stack)-1].kind != mainAlbum {
		t.Errorf("album-row 'o' did not push album view: depth %d -> %d", depth, len(m.stack))
	}
}
