package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/core"
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

	m = send(m, runes("1"))
	if got := m.FocusedPanel(); got != "Library" {
		t.Errorf("after '1' focus = %q, want Library", got)
	}

	// tab cycles Library -> Playlists -> Queue -> Main -> Library.
	want := []string{"Playlists", "Queue", "Main", "Library"}
	for _, w := range want {
		m = send(m, tea.KeyMsg{Type: tea.KeyTab})
		if got := m.FocusedPanel(); got != w {
			t.Errorf("after tab focus = %q, want %q", got, w)
		}
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
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := m.FocusedPanel(); got != "Main" {
		t.Fatalf("after right focus = %q, want Main", got)
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
