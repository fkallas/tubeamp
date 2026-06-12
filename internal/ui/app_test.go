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
	m := New(config.Default(), theme.Default(), nil, nil, core.NewQueue(), nil)
	return send(m, tea.WindowSizeMsg{Width: w, Height: h})
}

// fakeLibrary is an in-memory libraryProvider for tests: it records the calls it
// receives and returns canned data, so the Library/Playlists wiring can be
// exercised without any network. A nil error field returns the canned slice.
type fakeLibrary struct {
	name      string
	signedIn  bool // the source's own verdict; independent of name (OAuth: "" + true)
	playlists []model.Playlist
	liked     []model.Track
	tracks    map[string][]model.Track // by playlist id

	accountErr  error
	playlistErr error
	likedErr    error
	tracksErr   error

	accountCalls   int
	playlistCalls  int
	likedCalls     int
	playlistID     string // last id passed to PlaylistTracks
	playlistTracks int
}

func (f *fakeLibrary) AccountInfo(context.Context) (string, bool, error) {
	f.accountCalls++
	return f.name, f.signedIn, f.accountErr
}

func (f *fakeLibrary) LibraryPlaylists(context.Context) ([]model.Playlist, error) {
	f.playlistCalls++
	return f.playlists, f.playlistErr
}

func (f *fakeLibrary) LikedSongs(context.Context) ([]model.Track, error) {
	f.likedCalls++
	return f.liked, f.likedErr
}

func (f *fakeLibrary) PlaylistTracks(_ context.Context, id string) ([]model.Track, error) {
	f.playlistTracks++
	f.playlistID = id
	return f.tracks[id], f.tracksErr
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
	// Across all rows the modal covers: every preserved left segment must be
	// non-blank, and at least one must keep a left-column side border. (Checked
	// over the union of covered rows rather than any single row, so the
	// assertion does not depend on the modal's exact height.)
	sawSideBorder := false
	for _, r := range diffRows {
		if r == len(base)-1 {
			continue // the bottom hint line legitimately changes wholesale
		}
		br, or := []rune(base[r]), []rune(over[r])
		lcp := 0
		for lcp < len(br) && lcp < len(or) && br[lcp] == or[lcp] {
			lcp++
		}
		if strings.TrimSpace(string(br[:lcp])) == "" {
			t.Fatalf("row %d: overlay blanked the background left of the box\nbase: %q\nover: %q",
				r, base[r], over[r])
		}
		if strings.Contains(string(br[:lcp]), "│") {
			sawSideBorder = true
		}
	}
	if !sawSideBorder {
		t.Error("no covered row preserved a left-column side border left of the modal")
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

// TestLogoVisibleAtLargeTerminal asserts that the "tubeamp" wordmark is present
// in the rendered view at a normal terminal size.
func TestLogoVisibleAtLargeTerminal(t *testing.T) {
	m := newTestModel(t, 120, 40)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "tubeamp") {
		t.Errorf("View(120×40) should show the wordmark; \"tubeamp\" not found in:\n%s", v)
	}
}

// TestLogoWordmarkAndIndicator asserts the single-row header renders the
// "tubeamp" wordmark and the right-aligned version, AND that the sign-in
// indicator rides the same row alongside them.
func TestLogoWordmarkAndIndicator(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	v := ansi.Strip(m.View())
	for _, want := range []string{"tubeamp", appVersion, "● Felipe Kallas"} {
		if !strings.Contains(v, want) {
			t.Errorf("header view missing %q in:\n%s", want, v)
		}
	}
}

// TestLogoTickAdvancesPhase asserts a logoTickMsg advances the colour-cycle phase
// and re-arms the tick, and that the rendered view still holds (no panic, the
// wordmark is intact and the dimensions are unchanged) afterwards.
func TestLogoTickAdvancesPhase(t *testing.T) {
	const w, h = 120, 40
	m := newTestModel(t, w, h)
	before := m.logoPhase

	updated, cmd := m.Update(logoTickMsg{})
	m = updated.(Model)
	if m.logoPhase != before+1 {
		t.Errorf("logoPhase = %d, want %d (advanced by one tick)", m.logoPhase, before+1)
	}
	if cmd == nil {
		t.Error("logoTickMsg did not re-arm the animation tick")
	}

	v := m.View()
	if !strings.Contains(ansi.Strip(v), "tubeamp") {
		t.Errorf("wordmark missing after a tick:\n%s", ansi.Strip(v))
	}
	lines := strings.Split(v, "\n")
	if len(lines) != h {
		t.Fatalf("after tick: view has %d rows, want %d", len(lines), h)
	}
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got != w {
			t.Errorf("after tick: line %d width = %d, want %d", i, got, w)
		}
	}
}

// TestLogoTickPausesWhileHeaderHidden: while the terminal is too short for the
// header the wordmark is never rendered, so the tick must stop re-arming (no
// 4 Hz wake-ups for an invisible animation); a resize that brings the header
// back re-arms exactly one tick.
func TestLogoTickPausesWhileHeaderHidden(t *testing.T) {
	m := newTestModel(t, 120, logoMinTermHeight-1) // header hidden

	updated, cmd := m.Update(logoTickMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Error("tick re-armed while the header is hidden")
	}

	// Growing the terminal back over the threshold re-arms the animation.
	updated2, cmd2 := m.Update(tea.WindowSizeMsg{Width: 120, Height: logoMinTermHeight})
	m = updated2.(Model)
	if cmd2 == nil {
		t.Fatal("resize that shows the header did not re-arm the tick")
	}

	// A further resize while a tick is already pending must not arm a second.
	_, cmd3 := m.Update(tea.WindowSizeMsg{Width: 120, Height: logoMinTermHeight + 5})
	if cmd3 != nil {
		t.Error("resize armed a second tick while one was already pending")
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

// TestSearchCursorStartsAtTop pins the fix for the cursor landing mid-list:
// when the albums vertical arrives before the songs and the user never moved
// the selection, the finished list must start at the top — not drift to
// wherever the albums section ends up after the songs are prepended.
func TestSearchCursorStartsAtTop(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()

	m.searchGen = 2
	m = send(m, albumSearchMsg{gen: 2, query: "x", albums: []model.Album{a}}) // albums land first
	m = send(m, searchResultMsg{gen: 2, query: "x", tracks: ts})              // songs land second
	if got := m.stack[len(m.stack)-1].cursor; got != 0 {
		t.Errorf("untouched cursor after both results = %d, want 0 (top of list)", got)
	}
}

// TestSearchCursorKeepsDeliberateAlbumSelection is the counterpart: a selection
// the user actually navigated to keeps its identity through the reorder.
func TestSearchCursorKeepsDeliberateAlbumSelection(t *testing.T) {
	m := newTestModel(t, 120, 40)
	a, ts := fakeAlbum()

	m.searchGen = 2
	m = send(m, albumSearchMsg{gen: 2, query: "x", albums: []model.Album{a}})
	m = send(m, runes("j")) // deliberate navigation within the results
	m = send(m, searchResultMsg{gen: 2, query: "x", tracks: ts})
	fr := m.stack[len(m.stack)-1]
	if fr.cursor != len(fr.tracks) {
		t.Errorf("deliberate album selection landed at %d, want %d (first album row)", fr.cursor, len(fr.tracks))
	}
}

// TestHalfPageJump covers vim-style ctrl+d/ctrl+u: a large jump that clamps at
// the list edges when the list is shorter than half a page.
func TestHalfPageJump(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = send(m, runes("4")) // focus main (mock Liked Songs, shorter than half a page)
	top := func() int { return m.stack[len(m.stack)-1].cursor }

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if n := len(m.stack[len(m.stack)-1].tracks); top() != n-1 {
		t.Errorf("ctrl+d cursor = %d, want clamped bottom %d", top(), n-1)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if top() != 0 {
		t.Errorf("ctrl+u cursor = %d, want 0", top())
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

// TestLogoVisibleAtRequiredSizes pins the one-row wordmark header at the sizes
// called out in the layout contract — 120×40, 100×30, and a short height — and
// asserts the wordmark shows while the rendered view still totals exactly the
// terminal height with full-width rows (the header never overflows or clips the
// panel area).
func TestLogoVisibleAtRequiredSizes(t *testing.T) {
	for _, tc := range []struct{ w, h int }{
		{120, 40},
		{100, 30},
		{minWidth, 24}, // a short height (still above the header threshold)
	} {
		m := newTestModel(t, tc.w, tc.h)
		raw := m.View()
		if !strings.Contains(ansi.Strip(raw), "tubeamp") {
			t.Errorf("%dx%d: wordmark not shown", tc.w, tc.h)
		}
		lines := strings.Split(raw, "\n")
		if len(lines) != tc.h {
			t.Errorf("%dx%d: view has %d rows, want exactly %d", tc.w, tc.h, len(lines), tc.h)
		}
		for i, ln := range lines {
			if got := lipgloss.Width(ln); got != tc.w {
				t.Errorf("%dx%d: line %d width = %d, want %d", tc.w, tc.h, i, got, tc.w)
				break
			}
		}
	}
}

// TestLayoutInvariantAcrossHeights sweeps heights and asserts the rendered view
// always totals exactly the terminal height with full-width rows — i.e. the
// one-row header never makes the panel area overflow or clip at the minimum
// sizes. The sign-in indicator is set to an account name wider than the terminal,
// so the sweep also proves the indicator truncates on the header row rather than
// widening it (an unclipped indicator would make JoinVertical pad every row past
// the terminal width and break the full-width frame invariant).
func TestLayoutInvariantAcrossHeights(t *testing.T) {
	for _, w := range []int{minWidth, 100, 120} {
		for h := minHeight; h <= minHeight+12; h++ {
			m := newTestModel(t, w, h)
			m = send(m, accountInfoMsg{name: strings.Repeat("N", 124), signedIn: true})
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

// TestLogoVisibilityBoundary pins the header's height boundary: the wordmark is
// shown at exactly logoMinTermHeight and suppressed one row below — and in both
// modes the rendered view still totals exactly the terminal height with
// full-width rows (the header row is reclaimed by the panel area when hidden).
func TestLogoVisibilityBoundary(t *testing.T) {
	for _, tc := range []struct {
		h       int
		visible bool
	}{
		{logoMinTermHeight, true},      // first height that shows the wordmark
		{logoMinTermHeight - 1, false}, // last height that hides it
	} {
		m := newTestModel(t, 120, tc.h)
		raw := m.View()
		if got := strings.Contains(ansi.Strip(raw), "tubeamp"); got != tc.visible {
			t.Errorf("h=%d: wordmark visible = %v, want %v", tc.h, got, tc.visible)
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

// TestAuthIndicatorInStatusRowWhenLogoHidden asserts that on the shortest
// terminal (header hidden, just below logoMinTermHeight) the sign-in indicator
// falls through to the bottom status line.
func TestAuthIndicatorInStatusRowWhenLogoHidden(t *testing.T) {
	m := newTestModel(t, 120, logoMinTermHeight-1) // header hidden
	m = send(m, accountInfoMsg{name: "Felipe Kallas", signedIn: true})
	v := ansi.Strip(m.View())
	if strings.Contains(v, "tubeamp") {
		t.Fatalf("header should be hidden at height %d", logoMinTermHeight-1)
	}
	if !strings.Contains(v, "● Felipe Kallas") {
		t.Errorf("indicator not shown in status row when header hidden:\n%s", v)
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), &fakeLibrary{})
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), &fakeLibrary{})
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), &fakeLibrary{})
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
	m = send(m, runes("j")) // user deliberately highlights a2 (no songs yet)

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
// with an ellipsis on the wordmark header row instead of the indicator vanishing
// entirely (its home when the header is visible).
func TestLogoIndicatorTruncatedAtNarrowWidth(t *testing.T) {
	m := newTestModel(t, 70, 40) // min width, header visible
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

// TestAutoRefresh_probeErrorAdoptsClient: when the import itself succeeded (the
// auth file was rewritten, a fresh client exists) but the confirmation probe
// failed (offline, timeout), the fresh client must be adopted — not discarded —
// and the toast must not claim the re-import failed. The resolved sign-in state
// stays untouched (unconfirmed), mirroring the startup accountInfoMsg handler.
func TestAutoRefresh_probeErrorAdoptsClient(t *testing.T) {
	fresh := ytm.NewClient(nil)
	m := staleModel(t, "chrome", func(browser string, _ int) reimportMsg {
		return reimportMsg{browser: browser, client: fresh, err: context.DeadlineExceeded}
	})

	updated, cmd := m.Update(accountInfoMsg{signedIn: false})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a reimport Cmd after an anonymous check")
	}
	m = send(m, cmd())

	if m.c != fresh {
		t.Error("fresh client not adopted after a probe-only failure")
	}
	if m.authSignedIn {
		t.Error("sign-in must stay unconfirmed after a failed probe")
	}
	if strings.Contains(m.status, "re-import failed") {
		t.Errorf("status = %q: a probe-only failure must not claim the re-import failed", m.status)
	}
	if !strings.Contains(m.status, "could not confirm sign-in") || m.statusErr {
		t.Errorf("status = %q (err=%v), want an unconfirmed notice", m.status, m.statusErr)
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

// TestAlbumViewSurvivesLateLibraryLoad: opening an album view while a library
// load is in flight invalidates that load — the trailing libTracksMsg must not
// setMain over the just-pushed album view. (The inverse ordering was already
// guarded: setMain bumps albumGen.) Reachable since 'o' works on queue/song
// rows: enter on a playlist, focus the queue, 'o' on a row, playlist lands.
func TestAlbumViewSurvivesLateLibraryLoad(t *testing.T) {
	m := newTestModel(t, 120, 40)

	// A library load is dispatched and still in flight at this generation…
	m.libGen++
	pending := m.libGen

	// …then an album fetch wins the race and opens the album view.
	m.albumGen++
	m = send(m, albumLoadMsg{gen: m.albumGen, open: true,
		album:  model.Album{BrowseID: "MPREb_x", Title: "Neon Nights"},
		tracks: []model.Track{{VideoID: "v1", Title: "Chrome Tears"}}})
	if top := m.stack[len(m.stack)-1]; top.kind != mainAlbum {
		t.Fatalf("setup: album view not on top (kind=%v)", top.kind)
	}

	// The library response trails in: it must be dropped, not replace the stack.
	m = send(m, libTracksMsg{gen: pending, title: "Liked Songs",
		tracks: []model.Track{{VideoID: "v2", Title: "Late Arrival"}}})
	top := m.stack[len(m.stack)-1]
	if top.kind != mainAlbum || top.title != "Neon Nights" {
		t.Errorf("late library load replaced the album view: kind=%v title=%q", top.kind, top.title)
	}
}

// TestOpenAlbumFromSongRow: 'o' on a song row carrying an AlbumID fires a
// GetAlbum and, when the result arrives, pushes the album view.
func TestOpenAlbumFromSongRow(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
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
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
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

// ── libraryProvider wiring (OAuth Data API / cookie library source) ──────────

// newLibModel builds a model whose library source is lib (a fakeLibrary in
// tests). The InnerTube client (search/playback) is a harmless anonymous client;
// neither touches the network.
func newLibModel(t *testing.T, lib libraryProvider) Model {
	t.Helper()
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), lib)
	return send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
}

// TestLibProviderAccountFillsIndicator asserts the startup sign-in check reads
// the channel name from lib.AccountInfo and renders it as "● <name>".
func TestLibProviderAccountFillsIndicator(t *testing.T) {
	f := &fakeLibrary{name: "Ada Lovelace", signedIn: true}
	m := newLibModel(t, f)

	// The Cmd Init fires resolves to this accountInfoMsg via lib.AccountInfo.
	m = send(m, accountInfoCmd(f)())

	if f.accountCalls != 1 {
		t.Errorf("lib.AccountInfo called %d times, want 1", f.accountCalls)
	}
	if !m.authChecked || !m.authSignedIn || m.authName != "Ada Lovelace" {
		t.Fatalf("indicator state: checked=%v signedIn=%v name=%q", m.authChecked, m.authSignedIn, m.authName)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "● Ada Lovelace") {
		t.Errorf("view missing signed-in indicator from lib.AccountInfo:\n%s", v)
	}
}

// TestLibProviderOAuthNoChannelStillSignedIn covers the OAuth account that has
// no YouTube channel: ytdata.AccountInfo resolves (name "", signedIn true), and
// the UI must read that as a live session — the "● signed in" fallback indicator
// (never "○ not signed in"), the startup playlists fill firing, and no cookie
// re-import even with a leftover auth file + remembered browser.
func TestLibProviderOAuthNoChannelStillSignedIn(t *testing.T) {
	f := &fakeLibrary{
		signedIn:  true, // live OAuth session; no channel => name ""
		playlists: []model.Playlist{{ID: "PLreal", Title: "Road Mix"}},
	}
	m := newLibModel(t, f)
	// A leftover cookie auth file + remembered browser must NOT trigger a
	// re-import while the OAuth library session is live.
	m.hasAuth = true
	m.cfg.AuthBrowser = "chrome"

	mm, cmd := m.Update(accountInfoCmd(f)())
	m = mm.(Model)

	if !m.authChecked || !m.authSignedIn || m.authName != "" {
		t.Fatalf("indicator state: checked=%v signedIn=%v name=%q", m.authChecked, m.authSignedIn, m.authName)
	}
	if m.reimportTried {
		t.Error("signed-in (channel-less) OAuth session wrongly fired the cookie re-import")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "● signed in") {
		t.Errorf("view missing the signed-in fallback indicator:\n%s", v)
	}
	if cmd == nil {
		t.Fatal("signed-in (channel-less) account did not trigger the playlists fill")
	}
	msg := cmd()
	if _, ok := msg.(libPlaylistsMsg); !ok {
		t.Fatalf("follow-up cmd resolved %T, want libPlaylistsMsg", msg)
	}
	m = send(m, msg)
	if f.playlistCalls != 1 || !m.playlistsReal {
		t.Errorf("playlists fill: calls=%d real=%v, want 1/true", f.playlistCalls, m.playlistsReal)
	}
}

// TestLibProviderPlaylistsPanelFillsOnStartup asserts a signed-in startup check
// fires LibraryPlaylists against the library source and fills the Playlists panel.
func TestLibProviderPlaylistsPanelFillsOnStartup(t *testing.T) {
	f := &fakeLibrary{
		name:      "Ada",
		playlists: []model.Playlist{{ID: "PLreal", Title: "Road Mix", TrackCount: 9}},
	}
	m := newLibModel(t, f)

	// The signed-in account resolution triggers the one-shot playlists fill Cmd…
	mm, cmd := m.Update(accountInfoMsg{name: "Ada", signedIn: true})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("signed-in account did not trigger a LibraryPlaylists fill")
	}
	// …run it (it calls the fake) and feed the result back.
	m = send(m, cmd())

	if f.playlistCalls != 1 {
		t.Errorf("lib.LibraryPlaylists called %d times, want 1", f.playlistCalls)
	}
	if !m.playlistsReal || len(m.playlists) != 1 || m.playlists[0].Title != "Road Mix" {
		t.Fatalf("panel not filled from lib: real=%v playlists=%+v", m.playlistsReal, m.playlists)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Road Mix") || strings.Contains(v, "Focus Deep Work") {
		t.Errorf("Playlists panel did not replace mock with the real fill:\n%s", v)
	}
}

// TestLibProviderLikedSongsLoadsIntoMainView asserts Library→"Liked Songs" enter
// loads lib.LikedSongs into the focused main view.
func TestLibProviderLikedSongsLoadsIntoMainView(t *testing.T) {
	f := &fakeLibrary{
		liked: []model.Track{{VideoID: "v1", Title: "Liked One"}, {VideoID: "v2", Title: "Liked Two"}},
	}
	m := newLibModel(t, f)
	m = send(m, accountInfoMsg{name: "Ada", signedIn: true})

	// Library is the default focus; the cursor sits on "Liked Songs".
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter on Liked Songs dispatched no LikedSongs command")
	}
	m = send(m, cmd())

	if f.likedCalls != 1 {
		t.Errorf("lib.LikedSongs called %d times, want 1", f.likedCalls)
	}
	if m.FocusedPanel() != "Main" {
		t.Fatalf("Liked Songs load did not focus the main view; focus=%q", m.FocusedPanel())
	}
	top := m.stack[len(m.stack)-1]
	if top.title != "Liked Songs" || len(top.tracks) != 2 {
		t.Fatalf("main frame = {title:%q tracks:%d}, want {Liked Songs, 2}", top.title, len(top.tracks))
	}
	if !strings.Contains(ansi.Strip(m.View()), "Liked One") {
		t.Errorf("main view missing the loaded liked song")
	}
}

// TestLibProviderOpenPlaylistCallsPlaylistTracks asserts opening a real playlist
// row calls lib.PlaylistTracks with the playlist id and shows its tracks.
func TestLibProviderOpenPlaylistCallsPlaylistTracks(t *testing.T) {
	f := &fakeLibrary{
		playlists: []model.Playlist{{ID: "PLroad", Title: "Road Mix"}},
		tracks:    map[string][]model.Track{"PLroad": {{VideoID: "v9", Title: "Open Road"}}},
	}
	m := newLibModel(t, f)
	m = send(m, accountInfoMsg{name: "Ada", signedIn: true})

	// Fill the panel with the real playlist, then open it.
	m.plGen = 1
	m = send(m, libPlaylistsMsg{gen: 1, playlists: f.playlists})
	m = send(m, runes("2")) // focus Playlists
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter on a real playlist row dispatched no PlaylistTracks command")
	}
	m = send(m, cmd())

	if f.playlistTracks != 1 || f.playlistID != "PLroad" {
		t.Errorf("PlaylistTracks calls=%d id=%q, want 1 and PLroad", f.playlistTracks, f.playlistID)
	}
	top := m.stack[len(m.stack)-1]
	if top.title != "Road Mix" || len(top.tracks) != 1 {
		t.Fatalf("main frame = {title:%q tracks:%d}, want {Road Mix, 1}", top.title, len(top.tracks))
	}
	if !strings.Contains(ansi.Strip(m.View()), "Open Road") {
		t.Errorf("main view missing the loaded playlist track")
	}
}

// TestNilLibKeepsMockAndSignInHint asserts that with no library source (nil
// interface) but a search client present, "Liked Songs" enter keeps the mock
// data and toasts the sign-in hint — and canLoadLibrary stays false.
func TestNilLibKeepsMockAndSignInHint(t *testing.T) {
	m := New(config.Default(), theme.Default(), nil, ytm.NewClient(nil), core.NewQueue(), nil)
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if m.canLoadLibrary() {
		t.Fatal("canLoadLibrary should be false with a nil library source")
	}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // enter on Liked Songs
	m = mm.(Model)
	if cmd != nil {
		t.Error("enter on Liked Songs with no library source dispatched a command")
	}
	if !strings.Contains(m.status, "sign in to load your library") {
		t.Errorf("status = %q, want the sign-in hint", m.status)
	}
	top := m.stack[len(m.stack)-1]
	if top.title != "Liked Songs" || len(top.tracks) == 0 {
		t.Errorf("nil-lib Liked Songs enter did not open mock tracks: title=%q tracks=%d", top.title, len(top.tracks))
	}
}

// TestDataAPITrackRendersWithoutAlbumOrDuration asserts a Data-API-style track
// (Album="" and Duration=0) renders in both the main track table and the player
// bar without a panic or a garbage time — the unknown total shows "--:--".
func TestDataAPITrackRendersWithoutAlbumOrDuration(t *testing.T) {
	m := newTestModel(t, 120, 40)
	bare := model.Track{VideoID: "v0", Title: "No Album No Duration", Artists: []string{"Some Artist"}}

	// Main-view track table.
	m.setMain("Liked Songs", []model.Track{bare})
	m.setFocus(focusMain)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "No Album No Duration") {
		t.Errorf("main view missing the bare track title:\n%s", v)
	}
	if strings.Contains(v, "-1:") || strings.Contains(v, ":-") {
		t.Errorf("main view rendered a garbage duration:\n%s", v)
	}

	// Player bar: a now-playing bare track shows "--:--" for the unknown total.
	m.nowPlaying = bare
	m.hasNow = true
	m.timePos = 0
	m.duration = 0
	pv := ansi.Strip(m.View())
	if !strings.Contains(pv, "--:--") {
		t.Errorf("player bar did not render --:-- for an unknown duration:\n%s", pv)
	}
}
