// Package ui implements the tubeamp terminal UI: a Lazygit-inspired layout with
// a left column (library, playlists, queue), a contextual main track list, a
// player bar, and modal overlays (help, search, theme picker). It is the root
// Bubble Tea model; all IO (player commands, HTTP search, art fetch) happens in
// commands so Update never blocks.
package ui

import (
	"context"
	"image"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fkallas/tubeamp/internal/art"
	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/core"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/player"
	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ui/keymap"
	"github.com/fkallas/tubeamp/internal/ui/overlay"
	"github.com/fkallas/tubeamp/internal/ui/panels"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// minWidth / minHeight are the smallest terminal the UI renders into; below
// this a centered notice is shown instead.
const (
	minWidth  = 70
	minHeight = 20
)

// focusArea identifies which panel currently has keyboard focus.
type focusArea int

const (
	focusLibrary focusArea = iota
	focusPlaylists
	focusQueue
	focusMain
)

// overlayKind identifies which modal overlay (if any) is open.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlaySearch
	overlayTheme
)

// mainContent is one frame of the main-view stack: a titled track list with its
// own selection cursor. esc pops the stack (e.g. search results -> previous).
type mainContent struct {
	title  string
	tracks []model.Track
	cursor int
}

// Model is the root tea.Model for tubeamp.
type Model struct {
	cfg *config.Config
	th  *theme.Theme
	p   *player.Player // may be nil => playback disabled
	c   *ytm.Client    // may be nil => search disabled
	q   *core.Queue

	keys keymap.KeyMap

	width, height int

	focus focusArea

	// Left-column selection cursors.
	libItems    []string
	libCursor   int
	playlists   []model.Playlist
	plCursor    int
	queueCursor int

	// Main-view stack (top = stack[len-1]).
	stack []mainContent

	// searchGen monotonically tags the most recently-issued search. A result
	// is applied only if its tag still matches, so a stale search (one the user
	// has since superseded or navigated away from) never steals the view/focus.
	searchGen int

	// Overlays.
	overlay   overlayKind
	help      overlay.Help
	search    overlay.Search
	themeMenu overlay.ThemeMenu

	// Theme-picker transient state.
	loadedThemes map[string]*theme.Theme
	prevTheme    *theme.Theme

	// Playback state mirrored from player events.
	playbackOff bool // true once the player has closed
	nowPlaying  model.Track
	hasNow      bool
	paused      bool
	muted       bool
	volume      int
	timePos     float64
	duration    float64

	// Cover-art. Decoded thumbnails are cached by videoID so switching themes
	// re-renders locally instead of re-downloading; rendered strings are cached
	// by videoID+theme name; artInflight dedupes concurrent fetches per track.
	imgCache    map[string]image.Image
	artCache    map[string]string
	artInflight map[string]struct{}
	artBlock    string

	// Transient status / toast line.
	status    string
	statusErr bool
}

// New constructs the root model. p (player) and c (ytm client) may each be nil,
// in which case the affected features are disabled with a status-line notice.
func New(cfg *config.Config, th *theme.Theme, p *player.Player, c *ytm.Client, q *core.Queue) Model {
	m := Model{
		cfg:          cfg,
		th:           th,
		p:            p,
		c:            c,
		q:            q,
		keys:         keymap.Default(),
		focus:        focusLibrary,
		libItems:     libraryItems(),
		playlists:    mockPlaylists(),
		search:       overlay.NewSearch(),
		loadedThemes: map[string]*theme.Theme{},
		imgCache:     map[string]image.Image{},
		artCache:     map[string]string{},
		artInflight:  map[string]struct{}{},
		volume:       cfg.Volume,
	}
	m.stack = []mainContent{{title: "Liked Songs", tracks: mockLibraryTracks("Liked Songs")}}
	if p == nil {
		m.setError("mpv not found — playback disabled")
	}
	return m
}

// Init starts the player-event bridge when a player is present.
func (m Model) Init() tea.Cmd {
	if m.p != nil {
		return listenPlayer(m.p)
	}
	return nil
}

// FocusedPanel reports the focused panel as "Library", "Playlists", "Queue" or
// "Main". Exposed as a test/debug surface for asserting focus changes.
func (m Model) FocusedPanel() string {
	switch m.focus {
	case focusLibrary:
		return "Library"
	case focusPlaylists:
		return "Playlists"
	case focusQueue:
		return "Queue"
	default:
		return "Main"
	}
}

// ── Messages & commands ─────────────────────────────────────────────────────

type playerEventMsg player.Event
type playerClosedMsg struct{}
type artMsg struct {
	videoID string
	img     image.Image
	err     error
}
type searchResultMsg struct {
	gen    int
	query  string
	tracks []model.Track
	err    error
}
type themesLoadedMsg struct {
	names  []string
	themes map[string]*theme.Theme
}
type statusMsg struct {
	text  string
	isErr bool
}

// listenPlayer receives one player event per command and is re-issued after
// each event so the stream keeps flowing without blocking Update.
func listenPlayer(p *player.Player) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-p.Events()
		if !ok {
			return playerClosedMsg{}
		}
		return playerEventMsg(ev)
	}
}

func loadCmd(p *player.Player, url string) tea.Cmd {
	return func() tea.Msg {
		if err := p.Load(url); err != nil {
			return statusMsg{text: "playback error: " + err.Error(), isErr: true}
		}
		return nil
	}
}

func togglePauseCmd(p *player.Player) tea.Cmd {
	return func() tea.Msg { _ = p.TogglePause(); return nil }
}

func toggleMuteCmd(p *player.Player) tea.Cmd {
	return func() tea.Msg { _ = p.ToggleMute(); return nil }
}

func seekCmd(p *player.Player, off float64) tea.Cmd {
	return func() tea.Msg { _ = p.Seek(off); return nil }
}

func volumeCmd(p *player.Player, v int) tea.Cmd {
	return func() tea.Msg { _ = p.SetVolume(v); return nil }
}

// artFetchCmd downloads (only) the track's thumbnail; the decoded image is
// rendered to half-blocks back in Update so a theme change re-renders locally
// instead of re-downloading.
func artFetchCmd(t model.Track) tea.Cmd {
	url := t.ThumbURL
	vid := t.VideoID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		img, err := art.Fetch(ctx, url)
		return artMsg{videoID: vid, img: img, err: err}
	}
}

func searchCmd(c *ytm.Client, query string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ts, err := c.Search(ctx, query)
		return searchResultMsg{gen: gen, query: query, tracks: ts, err: err}
	}
}

func themesLoadCmd() tea.Cmd {
	return func() tea.Msg {
		dir := config.ThemesDir()
		loaded := map[string]*theme.Theme{}
		var valid []string
		for _, n := range theme.List(dir) {
			t, err := theme.Load(n, dir)
			if err != nil {
				continue // skip failures
			}
			loaded[n] = t
			valid = append(valid, n)
		}
		return themesLoadedMsg{names: valid, themes: loaded}
	}
}

// saveConfigCmd persists the config from a goroutine. It takes a value copy so
// the marshalling read never races a concurrent write to the live *config.Config
// on the UI goroutine (e.g. re-picking a theme while a prior save is in flight).
func saveConfigCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if err := cfg.Save(); err != nil {
			return statusMsg{text: "could not save config: " + err.Error(), isErr: true}
		}
		return statusMsg{text: "theme saved"}
	}
}

// ── Update ──────────────────────────────────────────────────────────────────

// Update handles a single message. It never blocks: all IO is dispatched as
// commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.SetWidth(min(50, m.width-12))
		return m, nil

	case tea.KeyMsg:
		// ctrl+c always quits, even with an overlay open.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.overlay != overlayNone {
			return m.updateOverlay(msg)
		}
		return m.updateMain(msg)

	case playerEventMsg:
		return m.handlePlayerEvent(player.Event(msg))

	case playerClosedMsg:
		m.playbackOff = true
		m.p = nil
		m.setError("mpv closed — playback disabled")
		return m, nil

	case artMsg:
		delete(m.artInflight, msg.videoID)
		if msg.err != nil || msg.img == nil {
			return m, nil // keep the placeholder
		}
		m.imgCache[msg.videoID] = msg.img
		// Render once with the current theme and cache by videoID+theme.
		s := art.Render(msg.img, 8, 4, art.Options{Palette: m.th.Palette()})
		m.artCache[msg.videoID+"|"+m.th.Name] = s
		if m.hasNow && m.nowPlaying.VideoID == msg.videoID {
			m.artBlock = s
		}
		return m, nil

	case searchResultMsg:
		// Ignore a stale result: the user has since issued another search or
		// navigated to a different main view.
		if msg.gen != m.searchGen {
			return m, nil
		}
		m.status = ""
		switch {
		case msg.err != nil:
			m.setError("search failed: " + msg.err.Error())
		case len(msg.tracks) == 0:
			m.setStatus("no results for \"" + msg.query + "\"")
		default:
			m.pushMain("Search: \""+msg.query+"\"", msg.tracks)
			m.setFocus(focusMain)
		}
		return m, nil

	case themesLoadedMsg:
		m.loadedThemes = msg.themes
		m.themeMenu.Names = msg.names
		m.themeMenu.Loading = false
		m.themeMenu.Cursor = indexOf(msg.names, m.cfg.Theme)
		return m, nil

	case statusMsg:
		if msg.isErr {
			m.setError(msg.text)
		} else {
			m.setStatus(msg.text)
		}
		return m, nil
	}

	return m, nil
}

// handlePlayerEvent folds a player event into the model and re-arms the event
// listener.
func (m Model) handlePlayerEvent(ev player.Event) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch ev.Kind {
	case player.EvTimePos:
		m.timePos = ev.Float
	case player.EvDuration:
		if ev.Float > 0 {
			m.duration = ev.Float
		}
	case player.EvPause:
		m.paused = ev.Bool
	case player.EvVolume:
		m.volume = int(ev.Float)
	case player.EvMute:
		m.muted = ev.Bool
	case player.EvFileLoaded:
		m.paused = false
	case player.EvTrackEnded:
		if t, ok := m.q.Advance(); ok {
			m.queueCursor = m.q.Index()
			cmd = m.playTrack(t)
		} else {
			// Queue exhausted: go idle but keep the queue.
			m.hasNow = false
			m.paused = false
			m.timePos = 0
			m.duration = 0
			m.artBlock = ""
		}
	case player.EvError:
		if ev.Str != "" {
			m.setError("player: " + ev.Str)
		}
	}
	return m, tea.Batch(cmd, listenPlayer(m.p))
}

// updateMain handles keys when no overlay is open.
func (m Model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = "" // clear any transient toast on interaction
	k := m.keys
	var cmd tea.Cmd

	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit

	case key.Matches(msg, k.Help):
		m.overlay = overlayHelp
	case key.Matches(msg, k.Search):
		m.overlay = overlaySearch
		m.search.Reset()
		cmd = m.search.Focus()
	case key.Matches(msg, k.Theme):
		m.overlay = overlayTheme
		m.prevTheme = m.th
		m.themeMenu = overlay.ThemeMenu{Loading: true}
		cmd = themesLoadCmd()

	case key.Matches(msg, k.Focus1):
		m.setFocus(focusLibrary)
	case key.Matches(msg, k.Focus2):
		m.setFocus(focusPlaylists)
	case key.Matches(msg, k.Focus3):
		m.setFocus(focusQueue)
	case key.Matches(msg, k.Focus4):
		m.setFocus(focusMain)

	case key.Matches(msg, k.Esc):
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}

	case key.Matches(msg, k.Up):
		m.moveCursor(-1)
	case key.Matches(msg, k.Down):
		m.moveCursor(1)
	case key.Matches(msg, k.Top):
		m.cursorToEdge(true)
	case key.Matches(msg, k.Bottom):
		m.cursorToEdge(false)

	case key.Matches(msg, k.Enter):
		cmd = m.handleEnter()

	case key.Matches(msg, k.Space):
		cmd = m.playbackCmd(func(p *player.Player) tea.Cmd { return togglePauseCmd(p) })
	case key.Matches(msg, k.Next):
		cmd = m.skip(true)
	case key.Matches(msg, k.Prev):
		cmd = m.skip(false)
	case key.Matches(msg, k.SeekBack):
		cmd = m.playbackCmd(func(p *player.Player) tea.Cmd { return seekCmd(p, -5) })
	case key.Matches(msg, k.SeekFwd):
		cmd = m.playbackCmd(func(p *player.Player) tea.Cmd { return seekCmd(p, 5) })
	case key.Matches(msg, k.VolUp):
		cmd = m.playbackCmd(func(p *player.Player) tea.Cmd { return volumeCmd(p, m.volume+5) })
	case key.Matches(msg, k.VolDown):
		cmd = m.playbackCmd(func(p *player.Player) tea.Cmd { return volumeCmd(p, m.volume-5) })
	case key.Matches(msg, k.Mute):
		cmd = m.playbackCmd(func(p *player.Player) tea.Cmd { return toggleMuteCmd(p) })

	case key.Matches(msg, k.Append):
		if m.focus == focusMain {
			if t, ok := m.mainCurrent(); ok {
				m.q.Append(t)
				m.setStatus("queued: " + t.Title)
			}
		}
	case key.Matches(msg, k.InsertNext):
		if m.focus == focusMain {
			if t, ok := m.mainCurrent(); ok {
				m.q.InsertNext(t)
				m.setStatus("playing next: " + t.Title)
			}
		}
	case key.Matches(msg, k.Remove):
		if m.focus == focusQueue {
			m.removeFromQueue()
		}
	case key.Matches(msg, k.MoveUp):
		if m.focus == focusQueue && m.queueCursor > 0 {
			m.q.Move(m.queueCursor, m.queueCursor-1)
			m.queueCursor--
		}
	case key.Matches(msg, k.MoveDown):
		if m.focus == focusQueue && m.queueCursor < m.q.Len()-1 {
			m.q.Move(m.queueCursor, m.queueCursor+1)
			m.queueCursor++
		}
	case key.Matches(msg, k.ClearQueue):
		if m.focus == focusQueue {
			m.q.Clear()
			m.queueCursor = 0
		}
	}

	return m, cmd
}

// updateOverlay routes keys to the open overlay.
func (m Model) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayHelp:
		if key.Matches(msg, m.keys.Esc) || key.Matches(msg, m.keys.Help) {
			m.overlay = overlayNone
		}
		return m, nil

	case overlaySearch:
		switch {
		case key.Matches(msg, m.keys.Esc):
			m.overlay = overlayNone
			return m, nil
		case key.Matches(msg, m.keys.Enter):
			q := strings.TrimSpace(m.search.Value())
			m.overlay = overlayNone
			if q == "" {
				return m, nil
			}
			if m.c == nil {
				m.setError("search needs network/auth")
				return m, nil
			}
			m.searchGen++
			m.setStatus("searching…")
			return m, searchCmd(m.c, q, m.searchGen)
		default:
			cmd := m.search.Update(msg)
			return m, cmd
		}

	case overlayTheme:
		switch {
		case key.Matches(msg, m.keys.Esc):
			m.th = m.prevTheme
			m.overlay = overlayNone
			return m, m.refreshArt()
		case key.Matches(msg, m.keys.Enter):
			if !m.themeMenu.Loading {
				if name := m.themeMenu.Selected(); name != "" {
					if t, ok := m.loadedThemes[name]; ok {
						m.th = t
					}
					m.cfg.Theme = name
					m.overlay = overlayNone
					return m, tea.Batch(m.refreshArt(), saveConfigCmd(*m.cfg))
				}
			}
			m.overlay = overlayNone
			return m, nil
		case key.Matches(msg, m.keys.Down):
			m.themeMenu.Down()
			return m, m.applyThemePreview()
		case key.Matches(msg, m.keys.Up):
			m.themeMenu.Up()
			return m, m.applyThemePreview()
		}
		return m, nil
	}
	return m, nil
}

// ── Focus & cursor helpers ──────────────────────────────────────────────────

func (m *Model) setFocus(f focusArea) {
	m.focus = f
}

// listSize returns the cursor pointer and item count for the focused panel.
func (m *Model) listState() (*int, int) {
	switch m.focus {
	case focusLibrary:
		return &m.libCursor, len(m.libItems)
	case focusPlaylists:
		return &m.plCursor, len(m.playlists)
	case focusQueue:
		return &m.queueCursor, m.q.Len()
	case focusMain:
		top := &m.stack[len(m.stack)-1]
		return &top.cursor, len(top.tracks)
	}
	return nil, 0
}

func (m *Model) moveCursor(delta int) {
	cur, n := m.listState()
	if cur == nil || n == 0 {
		return
	}
	*cur += delta
	if *cur < 0 {
		*cur = 0
	}
	if *cur > n-1 {
		*cur = n - 1
	}
}

func (m *Model) cursorToEdge(top bool) {
	cur, n := m.listState()
	if cur == nil || n == 0 {
		return
	}
	if top {
		*cur = 0
	} else {
		*cur = n - 1
	}
}

// ── Actions ─────────────────────────────────────────────────────────────────

func (m *Model) handleEnter() tea.Cmd {
	switch m.focus {
	case focusLibrary:
		if m.libCursor >= 0 && m.libCursor < len(m.libItems) {
			name := m.libItems[m.libCursor]
			m.setMain(name, mockLibraryTracks(name))
			m.setFocus(focusMain)
		}
	case focusPlaylists:
		if m.plCursor >= 0 && m.plCursor < len(m.playlists) {
			pl := m.playlists[m.plCursor]
			m.setMain(pl.Title, mockPlaylistTracks(pl.ID))
			m.setFocus(focusMain)
		}
	case focusQueue:
		if t, ok := m.q.JumpTo(m.queueCursor); ok {
			return m.playTrack(t)
		}
	case focusMain:
		top := m.stack[len(m.stack)-1]
		if len(top.tracks) > 0 {
			m.q.Set(top.tracks, top.cursor)
			m.queueCursor = m.q.Index()
			if t, ok := m.q.Current(); ok {
				return m.playTrack(t)
			}
		}
	}
	return nil
}

// skip advances (forward) or rewinds (backward) the queue and plays the result.
func (m *Model) skip(forward bool) tea.Cmd {
	if m.p == nil {
		m.setError("mpv not found — playback disabled")
		return nil
	}
	var t model.Track
	var ok bool
	if forward {
		t, ok = m.q.Advance()
	} else {
		t, ok = m.q.Prev()
	}
	if !ok {
		return nil
	}
	m.queueCursor = m.q.Index()
	return m.playTrack(t)
}

// playbackCmd runs fn(player) when a player is present, otherwise sets the
// disabled notice.
func (m *Model) playbackCmd(fn func(*player.Player) tea.Cmd) tea.Cmd {
	if m.p == nil {
		m.setError("mpv not found — playback disabled")
		return nil
	}
	return fn(m.p)
}

// playTrack sets the now-playing track, primes the cover art, and issues the
// load command. In degraded mode (no player) it sets none of the now-playing
// state — showing a frozen progress bar and an active play glyph for audio that
// never starts would be misleading — and only surfaces the disabled notice.
func (m *Model) playTrack(t model.Track) tea.Cmd {
	if m.p == nil {
		m.setError("mpv not found — playback disabled")
		return nil
	}
	m.nowPlaying = t
	m.hasNow = true
	m.paused = false
	m.timePos = 0
	m.duration = t.Duration.Seconds()

	return tea.Batch(m.refreshArt(), loadCmd(m.p, t.URL()))
}

// refreshArt updates artBlock for the current track+theme. It prefers, in
// order: the cached render for this theme; a local re-render of the decoded
// image (no download — e.g. when only the theme changed); otherwise a
// placeholder plus a one-shot fetch command, deduped so bouncing the theme
// cursor or re-pressing enter never issues a second download for the same track.
func (m *Model) refreshArt() tea.Cmd {
	if !m.hasNow {
		m.artBlock = ""
		return nil
	}
	vid := m.nowPlaying.VideoID
	if s, ok := m.artCache[vid+"|"+m.th.Name]; ok {
		m.artBlock = s
		return nil
	}
	if img, ok := m.imgCache[vid]; ok {
		s := art.Render(img, 8, 4, art.Options{Palette: m.th.Palette()})
		m.artCache[vid+"|"+m.th.Name] = s
		m.artBlock = s
		return nil
	}
	m.artBlock = art.Placeholder(vid, 8, 4, art.Options{Palette: m.th.Palette()})
	if m.nowPlaying.ThumbURL == "" {
		return nil
	}
	if _, busy := m.artInflight[vid]; busy {
		return nil // a fetch for this track is already running
	}
	m.artInflight[vid] = struct{}{}
	return artFetchCmd(m.nowPlaying)
}

// applyThemePreview live-applies the highlighted theme and re-renders the art.
func (m *Model) applyThemePreview() tea.Cmd {
	name := m.themeMenu.Selected()
	if t, ok := m.loadedThemes[name]; ok {
		m.th = t
	}
	return m.refreshArt()
}

func (m *Model) mainCurrent() (model.Track, bool) {
	top := m.stack[len(m.stack)-1]
	if top.cursor < 0 || top.cursor >= len(top.tracks) {
		return model.Track{}, false
	}
	return top.tracks[top.cursor], true
}

func (m *Model) removeFromQueue() {
	n := m.q.Len()
	if n == 0 || m.queueCursor < 0 || m.queueCursor >= n {
		return
	}
	m.q.Remove(m.queueCursor)
	if m.queueCursor >= m.q.Len() {
		m.queueCursor = m.q.Len() - 1
	}
	if m.queueCursor < 0 {
		m.queueCursor = 0
	}
}

// setMain replaces the main-view stack with a single content frame. It also
// invalidates any in-flight search so a late result cannot replace the view the
// user just navigated to.
func (m *Model) setMain(title string, tracks []model.Track) {
	m.stack = []mainContent{{title: title, tracks: tracks}}
	m.searchGen++
}

// pushMain pushes a new content frame onto the main-view stack.
func (m *Model) pushMain(title string, tracks []model.Track) {
	m.stack = append(m.stack, mainContent{title: title, tracks: tracks})
}

func (m *Model) setStatus(s string) { m.status, m.statusErr = s, false }
func (m *Model) setError(s string)  { m.status, m.statusErr = s, true }

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return 0
}

// ── View ────────────────────────────────────────────────────────────────────

// View renders the whole UI.
func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return m.tooSmall()
	}

	// Logo header: 2 rows when terminal is tall enough, otherwise hidden.
	logo := renderLogo(m.th, m.width, m.height)
	logoOff := 0
	if logo != "" {
		logoOff = logoHeight
	}

	leftW := clamp(m.width*3/10, 24, 40)
	mainW := m.width - leftW
	// player bar (6) + hint line (1) + logo header rows
	topH := m.height - 7 - logoOff

	libH := len(m.libItems) + 2
	rem := topH - libH
	if rem < 6 {
		rem = 6
	}
	plH := rem / 2
	qH := rem - plH

	libBox := panels.Library(m.th, m.libItems, m.libCursor, leftW, libH, m.focus == focusLibrary)
	plBox := panels.Playlists(m.th, m.playlists, m.plCursor, leftW, plH, m.focus == focusPlaylists)
	qBox := panels.Queue(m.th, m.q.Items(), m.queueCursor, m.q.Index(), leftW, qH, m.focus == focusQueue)
	leftCol := lipgloss.JoinVertical(lipgloss.Left, libBox, plBox, qBox)

	top := m.stack[len(m.stack)-1]
	playingID := ""
	if m.hasNow {
		playingID = m.nowPlaying.VideoID
	}
	mainBox := panels.MainView(m.th, "4 "+top.title, top.tracks, top.cursor, playingID, mainW, topH, m.focus == focusMain)

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, mainBox)
	bar := panels.PlayerBar(m.th, m.playerState(), m.artBlock, m.width)
	hint := m.bottomLine()

	// Assemble: optional logo header then panels, player bar, hints.
	parts := make([]string, 0, 4)
	if logo != "" {
		parts = append(parts, logo)
	}
	parts = append(parts, topRow, bar, hint)
	base := lipgloss.JoinVertical(lipgloss.Left, parts...)

	if m.overlay != overlayNone {
		return CompositeCenter(m.renderOverlay(), base)
	}
	return base
}

func (m Model) playerState() panels.PlayerState {
	return panels.PlayerState{
		Track:    m.nowPlaying,
		HasTrack: m.hasNow,
		Paused:   m.paused,
		Muted:    m.muted,
		Volume:   m.volume,
		Elapsed:  m.timePos,
		Total:    m.duration,
	}
}

func (m Model) renderOverlay() string {
	switch m.overlay {
	case overlayHelp:
		return m.help.View(m.th, m.keys, m.width, m.height)
	case overlaySearch:
		return m.search.View(m.th, m.width, m.height)
	case overlayTheme:
		return m.themeMenu.View(m.th, m.width, m.height)
	}
	return ""
}

func (m Model) tooSmall() string {
	w, h := max(m.width, 1), max(m.height, 1)
	msg := m.th.ErrorStyle().Render("terminal too small") + "\n" +
		m.th.Muted().Render("resize to at least 70×20")
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
}

// bottomLine renders the single-line status/hint bar. A transient status toast
// takes precedence over the context hints.
func (m Model) bottomLine() string {
	if m.status != "" {
		st := m.th.AccentStyle()
		if m.statusErr {
			st = m.th.ErrorStyle()
		}
		return st.Render(panels.Clip(" "+m.status, m.width))
	}
	var parts []string
	for _, h := range m.contextHints() {
		parts = append(parts, h.key+" "+h.desc)
	}
	line := " " + strings.Join(parts, "  ·  ")
	return m.th.Muted().Render(panels.Clip(line, m.width))
}

type hint struct{ key, desc string }

// contextHints returns the 5-6 most relevant bindings for the current overlay
// or focused panel, sourced from the key map's help text.
func (m Model) contextHints() []hint {
	k := m.keys
	switch m.overlay {
	case overlayHelp:
		return []hint{{k.Esc.Help().Key, "close"}}
	case overlaySearch:
		return []hint{{k.Enter.Help().Key, "search"}, {k.Esc.Help().Key, "cancel"}}
	case overlayTheme:
		return []hint{{"↑/↓", "preview"}, {k.Enter.Help().Key, "keep"}, {k.Esc.Help().Key, "revert"}}
	}
	switch m.focus {
	case focusLibrary, focusPlaylists:
		return []hint{{"↑/↓", "move"}, {k.Enter.Help().Key, "open"}, {"1-4", "focus"},
			{k.Search.Help().Key, "search"}, {k.Theme.Help().Key, "theme"}, {k.Help.Help().Key, "help"}}
	case focusQueue:
		return []hint{{"↑/↓", "move"}, {k.Enter.Help().Key, "play"}, {k.Remove.Help().Key, "remove"},
			{"J/K", "reorder"}, {k.ClearQueue.Help().Key, "clear"}, {k.Help.Help().Key, "help"}}
	default: // focusMain
		return []hint{{"↑/↓", "move"}, {k.Enter.Help().Key, "play"}, {k.Append.Help().Key, "queue"},
			{k.InsertNext.Help().Key, "play next"}, {k.Search.Help().Key, "search"}, {k.Help.Help().Key, "help"}}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
