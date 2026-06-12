package ui

import (
	"context"
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

// TestLogoHiddenAtSmallTerminal asserts that the logo is suppressed when the
// terminal height is below logoMinTermHeight (24) — the wordmark must not appear.
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
// exactly logoMinTermHeight (24), hidden one row below (23) — and in both
// modes the rendered view still totals exactly the terminal height with
// full-width rows (the 2 logo rows are reclaimed by the panel area).
func TestLogoVisibilityBoundary(t *testing.T) {
	for _, tc := range []struct {
		h       int
		visible bool
	}{
		{logoMinTermHeight, true},      // 24: first height that shows the logo
		{logoMinTermHeight - 1, false}, // 23: last height that hides it
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
