// Package ui implements the tubeamp terminal UI: a Lazygit-inspired layout with
// a left column (library, playlists, queue), a contextual main track list, a
// player bar, and modal overlays (help, search, theme picker). It is the root
// Bubble Tea model; all IO (player commands, HTTP search, art fetch) happens in
// commands so Update never blocks.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fkallas/tubeamp/internal/art"
	"github.com/fkallas/tubeamp/internal/auth"
	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/core"
	"github.com/fkallas/tubeamp/internal/enrich"
	"github.com/fkallas/tubeamp/internal/history"
	"github.com/fkallas/tubeamp/internal/model"
	"github.com/fkallas/tubeamp/internal/player"
	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ui/keymap"
	"github.com/fkallas/tubeamp/internal/ui/overlay"
	"github.com/fkallas/tubeamp/internal/ui/panels"
	"github.com/fkallas/tubeamp/internal/ytdata"
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
	focusLyrics // only reachable while the lyrics panel is visible (see lyricsVisible)

	focusCount // number of focus areas, for the h/l cycle
)

// overlayKind identifies which modal overlay (if any) is open.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlaySearch
	overlayTheme
)

// mainKind tags what a main-view stack frame holds, which selects how it renders
// and how its selection cursor maps to items.
type mainKind int

const (
	mainTracks  mainKind = iota // a plain track list (library, playlist, etc.)
	mainSearch                  // search results: a Songs section then an Albums section
	mainAlbum                   // an album detail view (cover + header + track list)
	mainArtists                 // the Library "Artists" list: one row per primary artist
	mainAlbums                  // the Library "Albums" list: album rows (reuses the album-row flow)
)

// mainContent is one frame of the main-view stack: a titled list with its own
// selection cursor. esc pops the stack (e.g. search results -> previous). For a
// mainSearch frame the cursor addresses tracks first then albums; for a
// mainAlbum frame it addresses the album's tracks.
type mainContent struct {
	kind   mainKind
	title  string
	tracks []model.Track
	albums []model.Album        // mainSearch/mainAlbums: the album rows
	album  model.Album          // mainAlbum: the album being viewed
	groups []ytdata.ArtistGroup // mainArtists: the grouped library artists (name + tracks)
	cursor int
	// mainSearch Albums section is merged from two sources that arrive separately:
	// derivedAlbums (reconstructed from the full-catalog song hits) shown FIRST,
	// then verticalAlbums (the dedicated album-search vertical, degraded for
	// anonymous sessions), deduped by BrowseID. albums holds the merged result.
	derivedAlbums  []model.Album
	verticalAlbums []model.Album
	// searchGen ties a mainSearch frame to the search generation that produced
	// it, so a later result (songs and albums arrive separately) updates the same
	// frame instead of pushing a duplicate.
	searchGen int
	// cursorMoved records that the user actually navigated within this frame.
	// When the songs section lands after the albums vertical, an untouched
	// cursor resets to the top of the list; only a deliberate selection keeps
	// its identity through the reorder.
	cursorMoved bool
}

// libraryProvider is the seam through which the UI reads the user's real library
// (sign-in state, playlists, liked songs, a playlist's tracks). Both the
// OAuth-backed *ytdata.Client (durable, official Data API) and a cookie
// *ytm.Client satisfy it, so cmd/tubeamp can pick the source without the UI
// caring which. A nil interface means "no library source" → mock data + sign-in
// hint. Search, album browsing and playback resolution stay on *ytm.Client (m.c).
// AccountInfo lets each source report signedIn on its own terms: a cookie session
// is anonymous when the account menu is empty, while an OAuth session is live
// whenever the probe succeeds — even for an account with no YouTube channel
// (name "") — so signedIn must come from the source, never be derived from a
// non-empty name.
type libraryProvider interface {
	AccountInfo(context.Context) (name string, signedIn bool, err error)
	LibraryPlaylists(context.Context) ([]model.Playlist, error)
	LikedSongs(context.Context) ([]model.Track, error)
	PlaylistTracks(context.Context, string) ([]model.Track, error)
}

// librarySongsProvider is the OPTIONAL extension of libraryProvider that exposes
// the whole-library aggregate (LikedSongs ++ every owned playlist's tracks) that
// backs the derived Library sections — "Songs", "Artists" (grouped by primary
// artist) and "Albums" (grouped by enriched AlbumID). Only the OAuth-backed
// *ytdata.Client implements it; a cookie *ytm.Client does not, so on a cookie
// session those three sections stay on mock data. The UI reaches it by
// type-asserting m.lib, so libraryProvider itself stays satisfiable by both
// sources unchanged.
type librarySongsProvider interface {
	LibrarySongs(context.Context) ([]model.Track, error)
}

// Model is the root tea.Model for tubeamp.
type Model struct {
	cfg  *config.Config
	th   *theme.Theme
	p    *player.Player  // may be nil => playback disabled
	c    *ytm.Client     // may be nil => search disabled
	lib  libraryProvider // may be nil => no real library (mock data + sign-in hint)
	q    *core.Queue
	hist *history.Store   // may be nil => local play history disabled (History stays mock)
	enr  *enrich.Enricher // may be nil => no album/duration enrichment (cache-only is also nil-safe)

	keys keymap.KeyMap

	width, height int

	focus focusArea

	// Left-column selection cursors.
	libItems    []string
	libCursor   int
	playlists   []model.Playlist
	plCursor    int
	queueCursor int

	// playlistsReal flips when a LibraryPlaylists result has replaced the mock
	// playlists. Until then (sign-in check pending, fill in flight, or the fill
	// failed) the panel rows are mock data whose IDs must never reach a real
	// PlaylistTracks browse.
	playlistsReal bool

	// Main-view stack (top = stack[len-1]).
	stack []mainContent

	// searchGen monotonically tags the most recently-issued search. A result
	// is applied only if its tag still matches, so a stale search (one the user
	// has since superseded or navigated away from) never steals the view/focus.
	searchGen int

	// albumGen is the search-style stale guard for GetAlbum fetches: a late
	// result is discarded when a newer fetch was issued or the user navigated
	// away (esc / replacing the main view). Album covers need no generation —
	// they are content-addressed by browseID and cached on arrival.
	albumGen int

	// libGen guards real library loads that land in the main view (Liked Songs,
	// a playlist's tracks): a late result is dropped when the user has since
	// navigated elsewhere (a newer load, esc, or replacing the main view).
	// plGen separately guards the one-shot Playlists-panel fill so the two never
	// invalidate each other.
	libGen int
	plGen  int

	// Library "Albums" background enrichment. The Data API exposes neither a
	// song's album nor its duration, so the aggregate is enriched progressively
	// from the anonymous InnerTube `next` endpoint (internal/enrich). enrichTracks
	// is the aggregate being enriched in place; enrichQueue is the videoIDs still
	// to fetch (drained one chunk at a time, only one chunk in flight); enrichTotal
	// is the initial missing count for the "enriching albums… N/M" status. The
	// follow-up chunk Cmds are libGen-guarded, so a section change (esc, a new
	// search/section, opening an album) abandons the in-flight enrichment.
	enrichTracks []model.Track
	enrichQueue  []string
	enrichTotal  int

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

	// Album-view cover art. Large covers are cached separately from player-bar
	// art: decoded images by browseID, rendered AlbumCover blocks by
	// browseID+theme, with albumArtInflight deduping concurrent fetches.
	albumImgCache    map[string]image.Image
	albumArtCache    map[string]string
	albumArtInflight map[string]struct{}

	// Transient status / toast line.
	status    string
	statusErr bool

	// Sign-in indicator (one-shot AccountInfo check). hasAuth records whether an
	// auth file was loaded (the client carries a cookie); authChecked flips once
	// the AccountInfo Cmd has resolved without error, with authSignedIn/authName
	// holding the live result.
	hasAuth      bool
	authChecked  bool
	authSignedIn bool
	authName     string

	// Stale-session auto-refresh. When a startup AccountInfo (or a library load)
	// resolves anonymous while cfg.AuthBrowser is set and an auth file exists, the
	// model fires a ONE-SHOT re-import from that browser (reimportFn). reimportTried
	// guards against a reimport loop — it happens at most once per session.
	// reimportFn is the injectable seam: it reads the browser, rewrites the auth
	// file, rebuilds a client, and confirms with AccountInfo, returning the result;
	// tests stub it so no browser or network is touched.
	reimportFn    reimportFunc
	reimportTried bool

	// Lyrics panel. Resolved lyrics are cached by videoID so a replay/seek never
	// refetches (the loading entry dedupes in-flight fetches; results are always
	// recorded — see applyLyrics). When unfocused the highlighted line is driven
	// off m.timePos via lyrics.CurrentLine — no extra event wiring.
	lyricsCache map[string]lyricResult

	// Focused-lyrics scroll state, meaningful only while focusLyrics is active.
	// lyricsScroll is the unsynced top-line offset OR the synced peek-line index;
	// lyricsDetached marks synced auto-follow as paused by a peek-scroll;
	// lyricsResumeAt is the playback position (seconds) at which a detached panel
	// re-engages auto-follow — set on each scroll to timePos+lyricsFollowResumeSec
	// and checked on later EvTimePos events (see maybeResumeLyrics).
	lyricsScroll   int
	lyricsDetached bool
	lyricsResumeAt float64
}

// New constructs the root model. p (player), c (ytm client), lib (library
// source), hist (local play history) and enr (album/duration enricher) may each
// be nil, in which case the affected features are disabled (a status-line notice
// for player/search; mock data + sign-in hint for the library; the History
// section and play-recording become no-ops; the Albums section shows whatever the
// raw aggregate already carries with no background enrichment). lib must be an
// untyped nil when there is no library source — a typed nil pointer would make the
// m.lib != nil guard wrongly fire.
func New(cfg *config.Config, th *theme.Theme, p *player.Player, c *ytm.Client, q *core.Queue, lib libraryProvider, hist *history.Store, enr *enrich.Enricher) Model {
	m := Model{
		cfg:              cfg,
		th:               th,
		p:                p,
		c:                c,
		lib:              lib,
		q:                q,
		hist:             hist,
		enr:              enr,
		keys:             keymap.Default(),
		focus:            focusLibrary,
		libItems:         libraryItems(),
		playlists:        mockPlaylists(),
		search:           overlay.NewSearch(),
		loadedThemes:     map[string]*theme.Theme{},
		imgCache:         map[string]image.Image{},
		artCache:         map[string]string{},
		artInflight:      map[string]struct{}{},
		albumImgCache:    map[string]image.Image{},
		albumArtCache:    map[string]string{},
		albumArtInflight: map[string]struct{}{},
		lyricsCache:      map[string]lyricResult{},
		volume:           cfg.Volume,
		hasAuth:          c != nil && c.Authenticated(),
		reimportFn:       defaultReimport,
	}
	m.stack = []mainContent{{title: "Liked Songs", tracks: mockLibraryTracks("Liked Songs")}}
	if p == nil {
		m.setError("mpv not found — playback disabled")
	} else {
		// Attaching to a live daemon: rebuild the queue and now-playing state so
		// an in-progress track shows in the player bar and queue panel at once.
		m.applySnapshot()
	}
	return m
}

// applySnapshot seeds the UI's mirror (queue + transport state) from the daemon
// on attach. It is a best-effort, synchronous read so the very first render
// already reflects whatever mpv is playing.
func (m *Model) applySnapshot() {
	s, err := m.p.Snapshot()
	if err != nil {
		return
	}
	m.q.Set(s.Tracks, 0)
	m.q.SetIndex(s.PlaylistPos)
	m.paused = s.Paused
	m.muted = s.Mute
	m.timePos = s.TimePos
	m.duration = s.Duration
	if s.Volume > 0 {
		m.volume = s.Volume
	}
	if s.PlaylistPos >= 0 {
		if t, ok := m.q.Current(); ok {
			m.nowPlaying = t
			m.hasNow = true
			m.queueCursor = s.PlaylistPos
			if m.duration == 0 {
				m.duration = t.Duration.Seconds()
			}
			m.artBlock = art.Placeholder(t.VideoID, 8, 4, m.artOptions())
		}
	}
}

// Init starts the player-event bridge and, when a track is already playing
// (attached to a live daemon), kicks off its cover-art and lyrics fetches.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.p != nil {
		cmds = append(cmds, listenPlayer(m.p))
	}
	if m.hasNow {
		if m.nowPlaying.ThumbURL != "" {
			m.artInflight[m.nowPlaying.VideoID] = struct{}{}
			cmds = append(cmds, artFetchCmd(m.nowPlaying))
		}
		// Kick off the lyrics lookup for the already-playing (attached) track.
		m.lyricsCache[m.nowPlaying.VideoID] = lyricResult{status: lyricLoading}
		cmds = append(cmds, fetchLyricsCmd(m.c, m.nowPlaying))
	}
	// One-shot sign-in check against the library source: surfaces the account
	// name (or a stale-cookie / not-signed-in indicator) in the logo/status area.
	// No re-check is issued. With no library source the indicator stays blank.
	if m.lib != nil {
		cmds = append(cmds, accountInfoCmd(m.lib))
	}
	return tea.Batch(cmds...)
}

// FocusedPanel reports the focused panel as "Library", "Playlists", "Queue",
// "Main" or "Lyrics". Exposed as a test/debug surface for asserting focus changes.
func (m Model) FocusedPanel() string {
	switch m.focus {
	case focusLibrary:
		return "Library"
	case focusPlaylists:
		return "Playlists"
	case focusQueue:
		return "Queue"
	case focusLyrics:
		return "Lyrics"
	default:
		return "Main"
	}
}

// LyricsScrollOffset reports the focused lyrics panel's current scroll offset
// (the unsynced top-line offset, or the synced peek-line index while detached).
// Exposed for tests asserting the focusable-lyrics scroll behaviour.
func (m Model) LyricsScrollOffset() int { return m.lyricsScroll }

// LyricsDetached reports whether synced lyrics auto-follow is currently paused
// by a focused peek-scroll. Exposed for tests.
func (m Model) LyricsDetached() bool { return m.lyricsDetached }

// ── Messages & commands ─────────────────────────────────────────────────────

type playerEventMsg player.Event
type playerClosedMsg struct{}

type artMsg struct {
	videoID string
	img     image.Image
	err     error
}
type searchResultMsg struct {
	gen           int
	query         string
	tracks        []model.Track
	derivedAlbums []model.Album // albums reconstructed from the song hits (catalog-complete)
	err           error
}
type albumSearchMsg struct {
	gen    int
	query  string
	albums []model.Album
	err    error
}

// albumLoadMsg carries a GetAlbum result. open selects the action: true pushes
// the album view, false replaces the queue with the album and plays.
type albumLoadMsg struct {
	gen    int
	open   bool
	title  string
	album  model.Album
	tracks []model.Track
	err    error
}

// albumCoverMsg carries a downloaded album cover image for the album view.
type albumCoverMsg struct {
	browseID string
	img      image.Image
	err      error
}
type themesLoadedMsg struct {
	names  []string
	themes map[string]*theme.Theme
}
type statusMsg struct {
	text  string
	isErr bool
}

// accountInfoMsg carries the one-shot sign-in check result from the library
// source (lib.AccountInfo): name is the account/channel name (may be "" for a
// signed-in OAuth account with no YouTube channel) and signedIn is the source's
// own verdict; err is set when the check could not run (network down, or a
// revoked OAuth token), in which case the indicator stays unresolved.
type accountInfoMsg struct {
	name     string
	signedIn bool
	err      error
}

// reimportMsg carries the result of a one-shot stale-session re-import: a fresh
// client built from the re-imported cookies plus the AccountInfo it resolved to.
// err is non-nil when the import or write failed; signedIn is false when the
// re-imported cookies still resolved anonymous.
type reimportMsg struct {
	browser  string
	client   *ytm.Client
	name     string
	signedIn bool
	err      error
}

// reimportFunc is the injectable browser re-import operation. Given a browser and
// the account index, it imports cookies, rewrites the auth file, rebuilds a
// client, and confirms via AccountInfo, returning the outcome as a reimportMsg.
type reimportFunc func(browser string, authUser int) reimportMsg

// libPlaylistsMsg carries the one-shot LibraryPlaylists result that fills the
// Playlists panel for a signed-in session. gen ties it to the plGen that issued
// it; err is ytm.ErrNotSignedIn for an anonymous session (mock data is kept).
type libPlaylistsMsg struct {
	gen       int
	playlists []model.Playlist
	err       error
}

// libTracksMsg carries a real library track load (Liked Songs or a playlist's
// tracks) destined for the main view, titled by title. gen ties it to the libGen
// that issued it; err is ytm.ErrNotSignedIn for an anonymous session.
type libTracksMsg struct {
	gen    int
	title  string
	tracks []model.Track
	err    error
}

// libAggregateMsg carries a whole-library aggregate (lib.LibrarySongs) load,
// destined for one of the derived sections named by section ("Songs", "Artists"
// or "Albums"). gen ties it to the libGen that issued it; err is
// ytm.ErrNotSignedIn for an anonymous session, handled like the other library
// loads (downgrade + sign-in hint).
type libAggregateMsg struct {
	gen     int
	section string
	tracks  []model.Track
	err     error
}

// enrichResultMsg carries one background album-enrichment chunk's results
// (album + duration for some videoIDs). gen ties it to the libGen of the Albums
// view that started the enrichment, so a section change drops it.
type enrichResultMsg struct {
	gen     int
	details []enrich.Detail
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

// Queue mutations route through the daemon's playlist. Each returns a Cmd so
// Update never blocks; the mpv playlist is the source of truth and the UI
// reconciles its current index from EvPlaylistPos.

func replaceCmd(p *player.Player, ts []model.Track, start int) tea.Cmd {
	return func() tea.Msg {
		if err := p.PlaylistReplace(ts, start); err != nil {
			return statusMsg{text: "playback error: " + err.Error(), isErr: true}
		}
		return nil
	}
}

func appendCmd(p *player.Player, t model.Track) tea.Cmd {
	return func() tea.Msg { _ = p.PlaylistAppend(t); return nil }
}

// insertNextCmd appends then moves the new entry to sit right after the current
// one (the daemon has no single insert-next primitive). from is the index the
// appended track lands at; to is the desired slot.
func insertNextCmd(p *player.Player, t model.Track, from, to int) tea.Cmd {
	return func() tea.Msg {
		if err := p.PlaylistAppend(t); err != nil {
			return statusMsg{text: "playback error: " + err.Error(), isErr: true}
		}
		if to >= 0 && to != from {
			_ = p.PlaylistMove(from, to)
		}
		return nil
	}
}

func removeCmd(p *player.Player, i int) tea.Cmd {
	return func() tea.Msg { _ = p.PlaylistRemove(i); return nil }
}

func moveCmd(p *player.Player, i, j int) tea.Cmd {
	return func() tea.Msg { _ = p.PlaylistMove(i, j); return nil }
}

func jumpCmd(p *player.Player, i int) tea.Cmd {
	return func() tea.Msg { _ = p.PlaylistJump(i); return nil }
}

func nextCmd(p *player.Player) tea.Cmd {
	return func() tea.Msg { _ = p.Next(); return nil }
}

func prevCmd(p *player.Player) tea.Cmd {
	return func() tea.Msg { _ = p.Prev(); return nil }
}

func clearCmd(p *player.Player) tea.Cmd {
	return func() tea.Msg { _ = p.PlaylistClear(); return nil }
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

// accountInfoCmd runs the one-shot sign-in check against the library source. It
// is bounded by a short timeout so a dead network resolves quickly into an err
// (indicator stays unresolved) rather than hanging. signedIn comes from the
// source itself, NOT from a non-empty name: an OAuth account without a YouTube
// channel resolves (name "", signedIn true), which must read as signed in (the
// indicator falls back to "● signed in") rather than as no session at all.
func accountInfoCmd(lib libraryProvider) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		name, signedIn, err := lib.AccountInfo(ctx)
		return accountInfoMsg{name: name, signedIn: signedIn, err: err}
	}
}

// defaultReimport is the production reimportFunc: it imports the browser's
// cookies, rewrites the auth file via ytm's shared atomic 0600 writer, rebuilds a
// client bound to that file (so future rotations persist), and confirms the
// session with a bounded AccountInfo probe.
func defaultReimport(browser string, authUser int) reimportMsg {
	header, _, err := auth.ImportFromBrowser(browser)
	if err != nil {
		return reimportMsg{browser: browser, err: err}
	}
	path := filepath.Join(config.DataDir(), "auth")
	if err := ytm.WriteAuthFile(path, header); err != nil {
		return reimportMsg{browser: browser, err: err}
	}
	a, err := ytm.LoadAuth(path)
	if err != nil {
		return reimportMsg{browser: browser, err: err}
	}
	c := ytm.NewClient(a)
	c.SetAuthUser(authUser)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	name, signedIn, err := c.AccountInfo(ctx)
	return reimportMsg{browser: browser, client: c, name: name, signedIn: signedIn, err: err}
}

// maybeReimportCmd fires the one-shot stale-session re-import when it is warranted
// and has not run yet: a remembered import browser (cfg.AuthBrowser), an auth file
// already present (hasAuth), and reimportTried still false. It flips reimportTried
// so the re-import happens at most once per session (no reimport loop), and
// returns nil when any precondition is unmet.
func (m *Model) maybeReimportCmd() tea.Cmd {
	if m.reimportTried || m.cfg == nil || m.cfg.AuthBrowser == "" || !m.hasAuth {
		return nil
	}
	m.reimportTried = true
	fn := m.reimportFn
	browser := m.cfg.AuthBrowser
	authUser := m.cfg.AuthUser
	return func() tea.Msg { return fn(browser, authUser) }
}

// libraryPlaylistsCmd fetches the signed-in user's playlists for the Playlists
// panel. plGen tags the result so a stale one is dropped.
func libraryPlaylistsCmd(lib libraryProvider, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pls, err := lib.LibraryPlaylists(ctx)
		return libPlaylistsMsg{gen: gen, playlists: pls, err: err}
	}
}

// likedSongsCmd fetches the Liked Songs auto-playlist into the main view; title
// is echoed back so the result handler can title the frame.
func likedSongsCmd(lib libraryProvider, title string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		ts, err := lib.LikedSongs(ctx)
		return libTracksMsg{gen: gen, title: title, tracks: ts, err: err}
	}
}

// playlistTracksCmd fetches a playlist's tracks into the main view, titled by
// the playlist name.
func playlistTracksCmd(lib libraryProvider, id, title string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		ts, err := lib.PlaylistTracks(ctx, id)
		return libTracksMsg{gen: gen, title: title, tracks: ts, err: err}
	}
}

// librarySongsCmd fetches the whole-library aggregate for a derived section
// (Songs/Artists/Albums) into the main view. The aggregate fans out one
// pagination per playlist, so it gets a generous timeout; section is echoed back
// so the handler knows how to render the result.
func librarySongsCmd(ls librarySongsProvider, section string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ts, err := ls.LibrarySongs(ctx)
		return libAggregateMsg{gen: gen, section: section, tracks: ts, err: err}
	}
}

// enrichChunk is the number of videoIDs fetched per background enrichment Cmd.
// Small chunks keep the album list updating responsively (and tubeamp light on
// InnerTube) while a large library fills in.
const enrichChunk = 8

// enrichChunkCmd enriches one chunk of videoIDs (album + duration) off the Update
// goroutine. EnrichMissing writes every success to the permanent cache; the
// returned details are also applied to the in-memory aggregate. gen guards the
// result against a section change.
func enrichChunkCmd(e *enrich.Enricher, ids []string, gen int) tea.Cmd {
	ids = append([]string(nil), ids...) // copy: the model's queue keeps mutating
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		details, _ := e.EnrichMissing(ctx, ids)
		return enrichResultMsg{gen: gen, details: details}
	}
}

func searchCmd(c *ytm.Client, query string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ts, albums, err := c.SearchWithAlbums(ctx, query)
		return searchResultMsg{gen: gen, query: query, tracks: ts, derivedAlbums: albums, err: err}
	}
}

func albumSearchCmd(c *ytm.Client, query string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		as, err := c.SearchAlbums(ctx, query)
		return albumSearchMsg{gen: gen, query: query, albums: as, err: err}
	}
}

// albumGetCmd browses an album page. open is echoed back in the result so the
// handler knows whether to play the album or open its view.
func albumGetCmd(c *ytm.Client, browseID, title string, open bool, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a, ts, err := c.GetAlbum(ctx, browseID)
		return albumLoadMsg{gen: gen, open: open, title: title, album: a, tracks: ts, err: err}
	}
}

// albumCoverCmd downloads (only) the album cover at px×px; rendering happens back
// in Update so a theme change re-renders locally without re-downloading.
func albumCoverCmd(browseID, thumbURL string) tea.Cmd {
	url := art.RewriteThumbURL(thumbURL, 64)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		img, err := art.Fetch(ctx, url)
		return albumCoverMsg{browseID: browseID, img: img, err: err}
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
		// A resize that hides the lyrics panel drops focus back to the main view;
		// one that grows the band re-clamps the unsynced scroll offset (its max
		// depends on the band's visible rows).
		m.reconcileLyricsFocus()
		m.clampLyricsScroll()
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
		s := art.Render(msg.img, 8, 4, m.artOptions())
		m.artCache[m.artKey(msg.videoID)] = s
		if m.hasNow && m.nowPlaying.VideoID == msg.videoID {
			m.artBlock = s
		}
		return m, nil

	case searchResultMsg:
		// Ignore a stale result: the user has since issued another search or
		// navigated away from these results.
		if msg.gen != m.searchGen {
			return m, nil
		}
		if msg.err != nil {
			m.setError(requestErrText("search failed: ", msg.err))
			return m, nil
		}
		m.status = ""
		fr := m.ensureSearchFrame(msg.query)
		// The songs section is about to be prepended and the albums reordered
		// derived-first. Only a selection the user deliberately made (cursorMoved)
		// keeps its identity through the reorder; an untouched cursor resets to
		// the top of the finished list instead of drifting to wherever the
		// albums section ends up.
		selAlbum := ""
		if fr.cursorMoved && len(fr.tracks) == 0 && fr.cursor >= 0 && fr.cursor < len(fr.albums) {
			selAlbum = fr.albums[fr.cursor].BrowseID
		}
		fr.tracks = msg.tracks
		fr.derivedAlbums = msg.derivedAlbums
		fr.albums = mergeSearchAlbums(fr.derivedAlbums, fr.verticalAlbums)
		if selAlbum != "" {
			for i, a := range fr.albums {
				if a.BrowseID == selAlbum {
					fr.cursor = len(fr.tracks) + i
					break
				}
			}
		} else if !fr.cursorMoved {
			fr.cursor = 0
		}
		return m, nil

	case albumSearchMsg:
		if msg.gen != m.searchGen {
			return m, nil
		}
		if msg.err != nil {
			m.setError(requestErrText("album search failed: ", msg.err))
			return m, nil
		}
		m.status = ""
		fr := m.ensureSearchFrame(msg.query)
		fr.verticalAlbums = msg.albums
		fr.albums = mergeSearchAlbums(fr.derivedAlbums, fr.verticalAlbums)
		return m, nil

	case albumLoadMsg:
		if msg.gen != m.albumGen {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.setError(requestErrText("could not load album: ", msg.err))
			return m, nil
		}
		if msg.open {
			m.pushAlbum(msg.album, msg.tracks)
			m.setFocus(focusMain)
			return m, m.ensureAlbumCover(msg.album)
		}
		// Play: replace the queue with the whole album from track 0.
		if len(msg.tracks) == 0 {
			m.setStatus("album has no tracks")
			return m, nil
		}
		m.q.Set(msg.tracks, 0)
		m.queueCursor = m.q.Index()
		if m.p == nil {
			m.setError("mpv not found — playback disabled")
			return m, nil
		}
		m.setStatus("Playing " + msg.album.Title)
		return m, tea.Batch(m.reflectCurrent(true), replaceCmd(m.p, msg.tracks, 0))

	case albumCoverMsg:
		delete(m.albumArtInflight, msg.browseID)
		if msg.err != nil || msg.img == nil {
			return m, nil // keep the placeholder
		}
		// Covers are content-addressed by browseID, so a late arrival is never
		// wrong: always cache it (decoded image + render for the current theme)
		// and let whichever view shows this album pick it up — including one
		// re-opened while the deduped fetch was still in flight.
		m.albumImgCache[msg.browseID] = msg.img
		m.albumArtCache[m.artKey(msg.browseID)] = art.Render(msg.img, panels.AlbumCoverCols, panels.AlbumCoverRows, m.artOptions())
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

	case accountInfoMsg:
		// A failed check (network down) leaves the indicator unresolved rather
		// than flashing a spurious "anonymous"; it is a one-shot, never retried.
		// One failure IS definitive: a revoked OAuth refresh token
		// (invalid_grant) fails every request the same way, so downgrade the
		// indicator and prompt the re-login instead of staying silent.
		if msg.err != nil {
			if invalidGrant(msg.err) {
				m.downgradeAuth()
				m.setError(loginHint)
			}
			return m, nil
		}
		m.authChecked = true
		m.authSignedIn = msg.signedIn
		m.authName = msg.name
		// Now that we know the session is live, fill the Playlists panel with the
		// user's real playlists. An anonymous session keeps the mock data — but if
		// we remember a browser to re-import from, try a one-shot refresh first.
		if msg.signedIn && m.lib != nil {
			m.plGen++
			return m, libraryPlaylistsCmd(m.lib, m.plGen)
		}
		return m, m.maybeReimportCmd()

	case libPlaylistsMsg:
		if msg.gen != m.plGen {
			return m, nil
		}
		if msg.err != nil {
			if errors.Is(msg.err, ytm.ErrNotSignedIn) {
				m.downgradeAuth()
				m.setError("sign in to load your library — see README")
				// The cookie rotated mid-session; try a one-shot refresh from the
				// remembered browser (replaces the hint on success).
				return m, m.maybeReimportCmd()
			}
			m.setError(requestErrText("could not load playlists: ", msg.err))
			return m, nil // keep the mock playlists
		}
		m.playlists = msg.playlists
		m.playlistsReal = true
		if m.plCursor >= len(m.playlists) {
			m.plCursor = 0
		}
		return m, nil

	case libTracksMsg:
		if msg.gen != m.libGen {
			return m, nil
		}
		if msg.err != nil {
			if errors.Is(msg.err, ytm.ErrNotSignedIn) {
				m.downgradeAuth()
				m.setError("sign in to load your library — see README")
				// The cookie rotated mid-session; try a one-shot refresh from the
				// remembered browser (replaces the hint on success).
				return m, m.maybeReimportCmd()
			}
			m.setError(requestErrText("could not load "+msg.title+": ", msg.err))
			return m, nil // keep the current (mock) view
		}
		m.status = ""
		m.setMain(msg.title, msg.tracks)
		m.setFocus(focusMain)
		return m, nil

	case libAggregateMsg:
		return m.applyAggregate(msg)

	case enrichResultMsg:
		return m.applyEnrich(msg)

	case reimportMsg:
		// A failed import/write/load (no fresh client) or a CONFIRMED anonymous
		// result falls back to a hint to run the explicit command; never retried
		// (reimportTried stays set).
		if msg.client == nil || (msg.err == nil && !msg.signedIn) {
			m.setError("re-import failed — run tubeamp -auth " + msg.browser)
			return m, nil
		}
		// The import succeeded and the auth file was rewritten; adopt the fresh
		// client even when only the confirmation probe failed (offline, timeout),
		// so in-process requests use — and persist rotations for — the new
		// cookies instead of the discarded stale jar. Mirroring the startup
		// accountInfoMsg handler, a probe error leaves the resolved state alone
		// rather than claiming anonymous or signed-in.
		oldC := m.c
		m.c = msg.client
		// When the library was cookie-backed (lib was this same InnerTube client),
		// repoint it at the refreshed client too so later library loads use the new
		// cookies. An OAuth (ytdata) library source is left untouched.
		if cookieLib, ok := m.lib.(*ytm.Client); ok && cookieLib == oldC {
			m.lib = msg.client
		}
		m.hasAuth = true
		if msg.err != nil {
			m.setStatus("re-imported from " + msg.browser + " — could not confirm sign-in")
			return m, nil
		}
		m.authChecked = true
		m.authSignedIn = true
		m.authName = msg.name
		m.setStatus("session refreshed from " + msg.browser)
		if m.lib != nil {
			m.plGen++
			return m, libraryPlaylistsCmd(m.lib, m.plGen)
		}
		return m, nil

	case lyricsMsg:
		m.applyLyrics(msg)
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
		// Idle-timer for the synced lyrics peek (no extra ticker): a detached peek
		// re-engages auto-follow once playback passes its resume deadline.
		m.maybeResumeLyrics()
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
	case player.EvPlaylistPos:
		// mpv advances the playlist itself; this is how the UI learns the
		// current track changed (auto-advance, or our own next/prev/jump).
		cmd = m.applyPlaylistPos(ev.Int)
	case player.EvTrackEnded:
		// No-op: mpv advances the playlist on its own and reports the new
		// position via EvPlaylistPos (including -1 at end-of-queue → idle).
	case player.EvError:
		if ev.Str != "" {
			m.setError("player: " + ev.Str)
		}
	}
	// Playback may have stopped (EvPlaylistPos -1), hiding the lyrics panel: drop
	// focus back to the main view if it was on the now-hidden panel.
	m.reconcileLyricsFocus()
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
	case key.Matches(msg, k.FocusNext):
		m.cycleFocus(1)
	case key.Matches(msg, k.FocusPrev):
		m.cycleFocus(-1)

	case key.Matches(msg, k.Esc):
		if m.focus == focusLyrics && m.lyricsDetached {
			// Consume esc: snap synced lyrics back to auto-follow WITHOUT popping
			// the main-view stack. esc only pops the stack when lyrics is not
			// focused, or is focused but not detached.
			m.reengageLyrics()
		} else if len(m.stack) > 1 {
			popped := m.stack[len(m.stack)-1]
			m.stack = m.stack[:len(m.stack)-1]
			// Popping search results invalidates any still-pending result for
			// them so a late songs/albums response cannot re-create the frame.
			if popped.kind == mainSearch {
				m.searchGen++
			}
			// Navigating back also invalidates any in-flight GetAlbum, so a
			// late album page can neither hijack the queue (enter on an album
			// row) nor push its view over the wrong context ('o'), and any
			// in-flight library load so it cannot replace the restored view.
			m.albumGen++
			m.libGen++
		}

	case key.Matches(msg, k.Up):
		if m.focus == focusLyrics {
			m.scrollLyrics(-1)
		} else {
			m.moveCursor(-1)
		}
	case key.Matches(msg, k.Down):
		if m.focus == focusLyrics {
			m.scrollLyrics(1)
		} else {
			m.moveCursor(1)
		}
	case key.Matches(msg, k.HalfPgUp):
		if m.focus == focusLyrics {
			m.scrollLyrics(-m.halfPageRows())
		} else {
			m.moveCursor(-m.halfPageRows())
		}
	case key.Matches(msg, k.HalfPgDn):
		if m.focus == focusLyrics {
			m.scrollLyrics(m.halfPageRows())
		} else {
			m.moveCursor(m.halfPageRows())
		}
	case key.Matches(msg, k.Top):
		if m.focus == focusLyrics {
			m.scrollLyricsEdge(true)
		} else {
			m.cursorToEdge(true)
		}
	case key.Matches(msg, k.Bottom):
		if m.focus == focusLyrics {
			m.scrollLyricsEdge(false)
		} else {
			m.cursorToEdge(false)
		}

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
				if m.p != nil {
					cmd = appendCmd(m.p, t)
				}
			}
		}
	case key.Matches(msg, k.Open):
		cmd = m.handleOpen()
	case key.Matches(msg, k.InsertNext):
		if m.focus == focusMain {
			if t, ok := m.mainCurrent(); ok {
				from := m.q.Len() // appended entry lands at the end
				to := from        // default (no current): append, no move
				if cur := m.q.Index(); cur >= 0 {
					to = cur + 1 // sit right after the current track
				}
				m.q.InsertNext(t)
				m.setStatus("playing next: " + t.Title)
				if m.p != nil {
					cmd = insertNextCmd(m.p, t, from, to)
				}
			}
		}
	case key.Matches(msg, k.Remove):
		if m.focus == focusQueue {
			cmd = m.removeFromQueue()
		}
	case key.Matches(msg, k.MoveUp):
		if m.focus == focusQueue && m.queueCursor > 0 {
			// Swap with the entry above: Move(i, i-1) relocates i to i-1.
			i, j := m.queueCursor, m.queueCursor-1
			m.q.Move(i, j)
			m.queueCursor--
			if m.p != nil {
				cmd = moveCmd(m.p, i, j)
			}
		}
	case key.Matches(msg, k.MoveDown):
		if m.focus == focusQueue && m.queueCursor < m.q.Len()-1 {
			// Swap with the entry below: Move(i+1, i) relocates i+1 to i.
			i, j := m.queueCursor+1, m.queueCursor
			m.q.Move(i, j)
			m.queueCursor++
			if m.p != nil {
				cmd = moveCmd(m.p, i, j)
			}
		}
	case key.Matches(msg, k.ClearQueue):
		if m.focus == focusQueue {
			m.q.Clear()
			m.queueCursor = 0
			m.hasNow = false
			m.artBlock = ""
			m.timePos = 0
			m.duration = 0
			if m.p != nil {
				cmd = clearCmd(m.p)
			}
		}
	}

	// Clearing the queue (or any action that hides the lyrics panel) drops focus
	// off the now-hidden lyrics panel.
	m.reconcileLyricsFocus()
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
			// Issuing a search supersedes any in-flight library load: its late
			// result must not setMain over the upcoming results frame.
			m.libGen++
			m.setStatus("searching…")
			// Songs and albums are fetched concurrently; whichever returns first
			// renders, the other fills in its section on arrival.
			return m, tea.Batch(searchCmd(m.c, q, m.searchGen), albumSearchCmd(m.c, q, m.searchGen))
		default:
			cmd := m.search.Update(msg)
			return m, cmd
		}

	case overlayTheme:
		switch {
		case key.Matches(msg, m.keys.Esc):
			m.th = m.prevTheme
			m.overlay = overlayNone
			m.refreshAlbumCover()
			return m, m.refreshArt()
		case key.Matches(msg, m.keys.Enter):
			if !m.themeMenu.Loading {
				if name := m.themeMenu.Selected(); name != "" {
					if t, ok := m.loadedThemes[name]; ok {
						m.th = t
					}
					m.cfg.Theme = name
					m.overlay = overlayNone
					m.refreshAlbumCover()
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
	// Leaving the lyrics panel re-engages synced auto-follow and resets its
	// scroll offset (detached state only ever applies while focusLyrics is active).
	if f != focusLyrics {
		m.reengageLyrics()
	}
	m.focus = f
}

// cycleFocus moves focus one step in dir (+1 forward, -1 backward) through the
// panel cycle Library→Playlists→Queue→Main→Lyrics→wrap, skipping the Lyrics
// panel whenever it is not currently visible.
func (m *Model) cycleFocus(dir int) {
	n := int(focusCount)
	f := int(m.focus)
	for i := 0; i < n; i++ {
		f = ((f+dir)%n + n) % n
		if focusArea(f) == focusLyrics && !m.lyricsVisible() {
			continue // skip the lyrics panel while it is hidden
		}
		break
	}
	m.setFocus(focusArea(f))
}

// reconcileLyricsFocus drops focus back to the main view (re-engaging follow)
// when the lyrics panel is focused but no longer visible — playback stopped or
// the terminal got too short.
func (m *Model) reconcileLyricsFocus() {
	if m.focus == focusLyrics && !m.lyricsVisible() {
		m.setFocus(focusMain)
	}
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
		if top.kind == mainArtists {
			return &top.cursor, len(top.groups)
		}
		// A search / albums frame's cursor runs through both sections (songs then
		// albums); for plain track and album-list frames one of them is empty.
		return &top.cursor, len(top.tracks) + len(top.albums)
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
	m.markCursorMoved()
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
	m.markCursorMoved()
}

// markCursorMoved records deliberate navigation on the top main-view frame, so
// in-flight search results know whether to respect the selection (see
// searchResultMsg).
func (m *Model) markCursorMoved() {
	if m.focus == focusMain {
		m.stack[len(m.stack)-1].cursorMoved = true
	}
}

// topAreaHeight is the height of the panel area between the wordmark header (when
// shown, >= logoMinTermHeight) and the player bar (6) + hint line (1). The left
// column and the right column (main view + lyrics band) both total this height.
func (m Model) topAreaHeight() int {
	logoOff := 0
	if m.height >= logoMinTermHeight {
		logoOff = logoHeight
	}
	return m.height - 7 - logoOff
}

// halfPageRows is the vim-style ctrl+d/ctrl+u jump distance: half the panel
// area's height. Short lists simply clamp at their edges.
func (m *Model) halfPageRows() int {
	return max(1, (m.topAreaHeight()-2)/2)
}

// ── Actions ─────────────────────────────────────────────────────────────────

func (m *Model) handleEnter() tea.Cmd {
	switch m.focus {
	case focusLibrary:
		if m.libCursor >= 0 && m.libCursor < len(m.libItems) {
			return m.openLibrarySection(m.libItems[m.libCursor])
		}
	case focusPlaylists:
		if m.plCursor >= 0 && m.plCursor < len(m.playlists) {
			pl := m.playlists[m.plCursor]
			// Only rows from a real LibraryPlaylists fill may hit the network:
			// while the panel still holds mock playlists (sign-in pending, fill
			// in flight, or fill failed) their fake IDs must not be browsed.
			if m.canLoadLibrary() && m.playlistsReal {
				m.libGen++
				m.setStatus("loading " + pl.Title + "…")
				m.setFocus(focusMain)
				return playlistTracksCmd(m.lib, pl.ID, pl.Title, m.libGen)
			}
			if m.playlistsReal {
				// Real playlists but the session has since resolved anonymous
				// (cookie rotated): there is no mock data for a real ID.
				m.setError("sign in to load your library — see README")
				return nil
			}
			m.setMain(pl.Title, mockPlaylistTracks(pl.ID))
			m.setFocus(focusMain)
			if m.c != nil && !m.authSignedIn {
				// A client is present but the session is anonymous.
				m.setError("sign in to load your library — see README")
			}
		}
	case focusQueue:
		if m.queueCursor < 0 || m.queueCursor >= m.q.Len() {
			return nil
		}
		if m.p == nil {
			m.setError("mpv not found — playback disabled")
			return nil
		}
		m.q.JumpTo(m.queueCursor)
		return tea.Batch(m.reflectCurrent(true), jumpCmd(m.p, m.queueCursor))
	case focusMain:
		top := m.stack[len(m.stack)-1]
		switch {
		case top.kind == mainArtists:
			// An artist row: push that artist's tracks (titled the artist name).
			if top.cursor >= 0 && top.cursor < len(top.groups) {
				g := top.groups[top.cursor]
				m.pushMain(g.Name, g.Tracks)
			}
			return nil
		case (top.kind == mainSearch || top.kind == mainAlbums) && top.cursor >= len(top.tracks):
			// An album row (search Albums section, or the Library Albums list):
			// fetch the album and play it.
			ai := top.cursor - len(top.tracks)
			if ai >= 0 && ai < len(top.albums) {
				return m.openAlbum(top.albums[ai], false)
			}
			return nil
		default:
			// A track row (plain list, search song, or album-view track): replace
			// the queue with this list starting at the selected track and play.
			return m.playTracks(top.tracks, top.cursor)
		}
	}
	return nil
}

// playTracks replaces the queue with ts starting at start and plays it. The
// local mirror is updated for instant feedback even when playback is disabled
// (nil player), in which case it only sets the disabled notice.
func (m *Model) playTracks(ts []model.Track, start int) tea.Cmd {
	if len(ts) == 0 {
		return nil
	}
	if start < 0 || start >= len(ts) {
		start = 0
	}
	m.q.Set(ts, start)
	m.queueCursor = m.q.Index()
	if m.p == nil {
		m.setError("mpv not found — playback disabled")
		return nil
	}
	return tea.Batch(m.reflectCurrent(true), replaceCmd(m.p, ts, start))
}

// handleOpen implements the `o` action: open an album view. On a search-results
// album row it opens that album by its own browseId (unchanged). On a SONG row —
// a plain track list (Liked Songs, a playlist), the search Songs section, or the
// Queue panel — it opens the album the song belongs to (its AlbumID); a song with
// no known album toasts instead. Both paths share the album-fetch generation
// guard and the album-view push. The album detail view is excluded: its rows
// already belong to the album on screen.
func (m *Model) handleOpen() tea.Cmd {
	switch m.focus {
	case focusMain:
		top := m.stack[len(m.stack)-1]
		if (top.kind == mainSearch || top.kind == mainAlbums) && top.cursor >= len(top.tracks) {
			// An album row (search Albums section, or the Library Albums list):
			// open by the album's own browseId.
			ai := top.cursor - len(top.tracks)
			if ai < 0 || ai >= len(top.albums) {
				return nil
			}
			return m.openAlbum(top.albums[ai], true)
		}
		if top.kind == mainAlbum || top.kind == mainArtists {
			return nil
		}
		if t, ok := m.mainCurrent(); ok {
			return m.openTrackAlbum(t)
		}
	case focusQueue:
		items := m.q.Items()
		if m.queueCursor >= 0 && m.queueCursor < len(items) {
			return m.openTrackAlbum(items[m.queueCursor])
		}
	}
	return nil
}

// openTrackAlbum opens the album a song belongs to (its AlbumID, an MPRE…
// browseId), pushing the album view via the same GetAlbum path as the album-row
// 'o'. A song carrying no album reference toasts "no album for this track".
func (m *Model) openTrackAlbum(t model.Track) tea.Cmd {
	if t.AlbumID == "" {
		m.setStatus("no album for this track")
		return nil
	}
	title := t.Album
	if title == "" {
		title = "album"
	}
	return m.openAlbum(model.Album{BrowseID: t.AlbumID, Title: title}, true)
}

// openAlbum fetches an album page via GetAlbum. open=true opens the album view;
// open=false plays the album. A nil client just sets a status notice.
func (m *Model) openAlbum(a model.Album, open bool) tea.Cmd {
	if m.c == nil {
		m.setError("album lookup needs network/auth")
		return nil
	}
	m.albumGen++
	m.setStatus("loading " + a.Title + "…")
	return albumGetCmd(m.c, a.BrowseID, a.Title, open, m.albumGen)
}

// skip moves to the next (forward) or previous track. The local mirror advances
// optimistically and the daemon's Next/Prev runs in a Cmd; EvPlaylistPos later
// reconciles the index. At a queue boundary it is a no-op.
func (m *Model) skip(forward bool) tea.Cmd {
	if m.p == nil {
		m.setError("mpv not found — playback disabled")
		return nil
	}
	var ok bool
	if forward {
		_, ok = m.q.Advance()
	} else {
		_, ok = m.q.Prev()
	}
	if !ok {
		return nil
	}
	m.queueCursor = m.q.Index()
	var pcmd tea.Cmd
	if forward {
		pcmd = nextCmd(m.p)
	} else {
		pcmd = prevCmd(m.p)
	}
	return tea.Batch(m.reflectCurrent(true), pcmd)
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

// reflectCurrent updates the now-playing display from the queue's current track,
// returning the cover-art command. resetTime zeroes the progress bar when the
// track actually changed (a fresh load starts at 0). When the queue has no
// current track it goes idle.
func (m *Model) reflectCurrent(resetTime bool) tea.Cmd {
	t, ok := m.q.Current()
	if !ok {
		m.hasNow = false
		if resetTime {
			m.timePos = 0
			m.duration = 0
		}
		m.artBlock = ""
		return nil
	}
	newTrack := !m.hasNow || m.nowPlaying.VideoID != t.VideoID
	m.nowPlaying = t
	m.hasNow = true
	m.paused = false
	if resetTime && newTrack {
		m.timePos = 0
		m.duration = t.Duration.Seconds()
	}
	art := m.refreshArt()
	if newTrack {
		// New track: reset any focused-lyrics scroll/detach, refresh the lyrics
		// panel alongside the cover art, and record the play in the local history.
		m.reengageLyrics()
		return tea.Batch(art, m.ensureLyrics(), m.recordHistoryCmd(t))
	}
	return art
}

// applyPlaylistPos reconciles the UI's current index with the daemon's
// playlist-pos (mpv advances the playlist itself). A position equal to the local
// index is a no-op (our own mutation already reflected it); -1 means end-of-queue
// idle.
func (m *Model) applyPlaylistPos(pos int) tea.Cmd {
	if pos == m.q.Index() {
		if pos < 0 {
			m.hasNow = false
		}
		return nil
	}
	m.q.SetIndex(pos)
	if pos < 0 {
		m.hasNow = false
		m.paused = false
		m.timePos = 0
		m.duration = 0
		m.artBlock = ""
		return nil
	}
	m.queueCursor = pos
	return m.reflectCurrent(true)
}

// artOptions returns the cover-art rendering options for the configured
// palette mode: "theme" snaps colors to the active theme's palette, while
// "auto" (the default) keeps the artwork's own colors, median-cut to a
// compact pixel-art palette.
func (m Model) artOptions() art.Options {
	if m.cfg != nil && m.cfg.ArtPalette == config.ArtPaletteTheme {
		return art.Options{Palette: m.th.Palette()}
	}
	return art.Options{PaletteSize: 16}
}

// artKey builds a render-cache key carrying everything the render depends on:
// in theme mode the theme name (a theme switch re-renders), in auto mode just
// the mode (renders are theme-independent).
func (m Model) artKey(id string) string {
	if m.cfg != nil && m.cfg.ArtPalette == config.ArtPaletteTheme {
		return id + "|theme:" + m.th.Name
	}
	return id + "|auto"
}

// refreshArt updates artBlock for the current track+palette mode. It prefers,
// in order: the cached render; a local re-render of the decoded image (no
// download — e.g. when only the theme changed); otherwise a placeholder plus a
// one-shot fetch command, deduped so bouncing the theme cursor or re-pressing
// enter never issues a second download for the same track.
func (m *Model) refreshArt() tea.Cmd {
	if !m.hasNow {
		m.artBlock = ""
		return nil
	}
	vid := m.nowPlaying.VideoID
	if s, ok := m.artCache[m.artKey(vid)]; ok {
		m.artBlock = s
		return nil
	}
	if img, ok := m.imgCache[vid]; ok {
		s := art.Render(img, 8, 4, m.artOptions())
		m.artCache[m.artKey(vid)] = s
		m.artBlock = s
		return nil
	}
	m.artBlock = art.Placeholder(vid, 8, 4, m.artOptions())
	if m.nowPlaying.ThumbURL == "" {
		return nil
	}
	if _, busy := m.artInflight[vid]; busy {
		return nil // a fetch for this track is already running
	}
	m.artInflight[vid] = struct{}{}
	return artFetchCmd(m.nowPlaying)
}

// applyThemePreview live-applies the highlighted theme and re-renders the art
// (player-bar cover and, when open, the album view cover).
func (m *Model) applyThemePreview() tea.Cmd {
	name := m.themeMenu.Selected()
	if t, ok := m.loadedThemes[name]; ok {
		m.th = t
	}
	m.refreshAlbumCover()
	return m.refreshArt()
}

func (m *Model) mainCurrent() (model.Track, bool) {
	top := m.stack[len(m.stack)-1]
	if top.cursor < 0 || top.cursor >= len(top.tracks) {
		return model.Track{}, false
	}
	return top.tracks[top.cursor], true
}

// removeFromQueue drops the entry under the queue cursor locally and on the
// daemon. Removing the currently-playing entry makes mpv advance to the next, so
// the now-playing display is refreshed from the new current track.
func (m *Model) removeFromQueue() tea.Cmd {
	n := m.q.Len()
	if n == 0 || m.queueCursor < 0 || m.queueCursor >= n {
		return nil
	}
	idx := m.queueCursor
	wasCurrent := idx == m.q.Index()
	m.q.Remove(idx)
	if m.queueCursor >= m.q.Len() {
		m.queueCursor = m.q.Len() - 1
	}
	if m.queueCursor < 0 {
		m.queueCursor = 0
	}
	var cmd tea.Cmd
	if wasCurrent && m.p != nil {
		cmd = m.reflectCurrent(true)
	}
	if m.p != nil {
		return tea.Batch(cmd, removeCmd(m.p, idx))
	}
	return cmd
}

// setMain replaces the main-view stack with a single content frame. It also
// invalidates any in-flight search, album, and library fetch so a late result
// cannot replace or cover the view the user just navigated to.
func (m *Model) setMain(title string, tracks []model.Track) {
	m.stack = []mainContent{{title: title, tracks: tracks}}
	m.searchGen++
	m.albumGen++
	m.libGen++
}

// pushMain pushes a new track-list content frame onto the main-view stack.
func (m *Model) pushMain(title string, tracks []model.Track) {
	m.stack = append(m.stack, mainContent{title: title, tracks: tracks})
}

// setArtists replaces the main-view stack with the Library "Artists" list. Like
// setMain it invalidates any in-flight search/album/library fetch so a late
// result cannot replace the view.
func (m *Model) setArtists(title string, groups []ytdata.ArtistGroup) {
	m.stack = []mainContent{{kind: mainArtists, title: title, groups: groups}}
	m.searchGen++
	m.albumGen++
	m.libGen++
}

// librarySource returns the library source as a librarySongsProvider when it
// exposes the whole-library aggregate (the OAuth-backed *ytdata.Client). A nil or
// cookie-only source yields (nil, false).
func (m Model) librarySource() (librarySongsProvider, bool) {
	if m.lib == nil {
		return nil, false
	}
	ls, ok := m.lib.(librarySongsProvider)
	return ls, ok
}

// openLibrarySection routes a Library-panel enter to the right loader. Real
// sources load live; with no source the section falls back to mock data (plus a
// sign-in hint when a search client is present and there is no library at all).
func (m *Model) openLibrarySection(name string) tea.Cmd {
	switch name {
	case "Liked Songs":
		return m.openLikedSongs(name)
	case "Songs", "Artists", "Albums":
		return m.openAggregateSection(name)
	case "History":
		return m.openHistory(name)
	default:
		m.setMain(name, mockLibraryTracks(name))
		m.setFocus(focusMain)
		return nil
	}
}

// openLikedSongs loads the Liked Songs auto-playlist from a real source, else
// falls back to the mock list (with a sign-in hint when a search client exists).
func (m *Model) openLikedSongs(name string) tea.Cmd {
	if m.canLoadLibrary() {
		m.libGen++
		m.setStatus("loading " + name + "…")
		m.setFocus(focusMain)
		return likedSongsCmd(m.lib, name, m.libGen)
	}
	m.setMain(name, mockLibraryTracks(name))
	m.setFocus(focusMain)
	if m.c != nil {
		m.setError("sign in to load your library — see README")
	}
	return nil
}

// openAggregateSection loads the whole-library aggregate for a derived section
// (Songs/Artists/Albums). Only a source exposing LibrarySongs (the OAuth Data API
// client) loads live; a nil/cookie source falls back to mock data.
func (m *Model) openAggregateSection(name string) tea.Cmd {
	if ls, ok := m.librarySource(); ok {
		m.libGen++
		m.setStatus("loading " + name + "…")
		m.setFocus(focusMain)
		return librarySongsCmd(ls, name, m.libGen)
	}
	m.setMain(name, mockLibraryTracks(name))
	m.setFocus(focusMain)
	if m.lib == nil && m.c != nil {
		m.setError("sign in to load your library — see README")
	}
	return nil
}

// openHistory loads tubeamp's own local play history into the main view. With no
// history store it falls back to the mock list.
func (m *Model) openHistory(name string) tea.Cmd {
	if m.hist != nil {
		m.setMain("History", m.hist.List())
		m.setFocus(focusMain)
		return nil
	}
	m.setMain(name, mockLibraryTracks(name))
	m.setFocus(focusMain)
	return nil
}

// applyAggregate folds a LibrarySongs result into the right derived section. A
// stale result (the user navigated away) is dropped; ErrNotSignedIn degrades like
// the other library loads. Cache-resident enrichment fills in albums/durations
// instantly; the Albums section then enriches the rest in the background.
func (m Model) applyAggregate(msg libAggregateMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.libGen {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, ytm.ErrNotSignedIn) {
			m.downgradeAuth()
			m.setError("sign in to load your library — see README")
			return m, m.maybeReimportCmd()
		}
		m.setError(requestErrText("could not load "+msg.section+": ", msg.err))
		return m, nil
	}
	m.status = ""
	switch msg.section {
	case "Songs":
		m.setMain("Songs", m.fillFromCache(msg.tracks))
		m.setFocus(focusMain)
		return m, nil
	case "Artists":
		m.setArtists("Artists", ytdata.GroupArtists(m.fillFromCache(msg.tracks)))
		m.setFocus(focusMain)
		return m, nil
	case "Albums":
		return m.startAlbumsSection(msg.tracks)
	}
	return m, nil
}

// fillFromCache returns tracks with any already-cached album/duration applied
// (instant, no network). With no enricher it returns the tracks unchanged.
func (m Model) fillFromCache(tracks []model.Track) []model.Track {
	if m.enr == nil {
		return tracks
	}
	filled, _ := m.enr.Fill(tracks)
	return filled
}

// startAlbumsSection builds the Library "Albums" list from the aggregate and
// kicks off progressive background enrichment for the tracks the Data API left
// without an album. It does NOT bump libGen (unlike setMain): the enrichment
// chunk Cmds ride the current libGen so they stay valid until the user navigates.
func (m Model) startAlbumsSection(aggregate []model.Track) (tea.Model, tea.Cmd) {
	filled := aggregate
	var missing []string
	if m.enr != nil {
		filled, missing = m.enr.Fill(aggregate)
	}
	m.enrichTracks = filled
	m.enrichQueue = missing
	m.enrichTotal = len(missing)

	// Replace the stack directly (keep libGen so enrichment stays valid) but
	// invalidate any in-flight search/album fetch so a late result cannot cover it.
	m.stack = []mainContent{{kind: mainAlbums, title: "Albums", albums: ytdata.AlbumsFromTracks(filled)}}
	m.searchGen++
	m.albumGen++
	m.setFocus(focusMain)

	if cmd := m.dispatchEnrichChunk(); cmd != nil {
		m.setStatus(m.enrichStatus())
		return m, cmd
	}
	m.status = ""
	return m, nil
}

// applyEnrich folds one background enrichment chunk into the aggregate, refreshes
// the album list, advances the queue, and fires the next chunk. A stale result
// (the user changed section) is dropped.
func (m Model) applyEnrich(msg enrichResultMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.libGen {
		return m, nil
	}
	if len(msg.details) > 0 {
		applyDetailsToTracks(m.enrichTracks, msg.details)
		if top := &m.stack[len(m.stack)-1]; top.kind == mainAlbums {
			top.albums = ytdata.AlbumsFromTracks(m.enrichTracks)
		}
	}
	// Drop the chunk just processed (success or not) so a failing id is never
	// retried forever, then fire the next chunk.
	n := enrichChunk
	if n > len(m.enrichQueue) {
		n = len(m.enrichQueue)
	}
	m.enrichQueue = m.enrichQueue[n:]
	if cmd := m.dispatchEnrichChunk(); cmd != nil {
		m.setStatus(m.enrichStatus())
		return m, cmd
	}
	m.status = ""
	return m, nil
}

// dispatchEnrichChunk returns a Cmd enriching the next queued chunk, or nil when
// there is nothing left to enrich (or no enricher).
func (m Model) dispatchEnrichChunk() tea.Cmd {
	if m.enr == nil || len(m.enrichQueue) == 0 {
		return nil
	}
	n := enrichChunk
	if n > len(m.enrichQueue) {
		n = len(m.enrichQueue)
	}
	return enrichChunkCmd(m.enr, m.enrichQueue[:n], m.libGen)
}

// enrichStatus renders the "enriching albums… N/M" progress line, N being the
// number of tracks resolved so far out of M still needing enrichment at the start.
func (m Model) enrichStatus() string {
	return fmt.Sprintf("enriching albums… %d/%d", m.enrichTotal-len(m.enrichQueue), m.enrichTotal)
}

// applyDetailsToTracks fills only the still-missing album/duration fields of any
// track matching a detail's videoID (source data is never clobbered). It mutates
// tracks in place.
func applyDetailsToTracks(tracks []model.Track, details []enrich.Detail) {
	byID := make(map[string]enrich.Detail, len(details))
	for _, d := range details {
		byID[d.VideoID] = d
	}
	for i := range tracks {
		d, ok := byID[tracks[i].VideoID]
		if !ok {
			continue
		}
		if tracks[i].Album == "" {
			tracks[i].Album = d.Album
		}
		if tracks[i].AlbumID == "" {
			tracks[i].AlbumID = d.AlbumID
		}
		if tracks[i].Duration == 0 {
			tracks[i].Duration = d.Duration
		}
	}
}

// recordHistoryCmd records a track to the local play history off the Update
// goroutine. A nil history store or an empty-VideoID track is a no-op (nil Cmd).
func (m *Model) recordHistoryCmd(t model.Track) tea.Cmd {
	if m.hist == nil || t.VideoID == "" {
		return nil
	}
	h := m.hist
	return func() tea.Msg {
		_ = h.Record(t)
		return nil
	}
}

// mergeSearchAlbums builds the rendered Albums section: the albums derived from
// the full-catalog song hits come first (they reflect releases the degraded
// album vertical withholds from anonymous sessions), then the album-vertical
// results, deduped by BrowseID. Order within each source is preserved. An album
// present in both sources keeps its derived-first position but takes the
// vertical's richer metadata (true album artists, Year, album cover) — the
// derived ref only carries song-row stand-ins.
func mergeSearchAlbums(derived, vertical []model.Album) []model.Album {
	index := make(map[string]int, len(derived)+len(vertical))
	merged := make([]model.Album, 0, len(derived)+len(vertical))
	for _, src := range [][]model.Album{derived, vertical} {
		for _, a := range src {
			if a.BrowseID == "" {
				continue
			}
			if i, ok := index[a.BrowseID]; ok {
				merged[i] = enrichAlbum(merged[i], a)
				continue
			}
			index[a.BrowseID] = len(merged)
			merged = append(merged, a)
		}
	}
	return merged
}

// enrichAlbum keeps dst's identity (BrowseID, Title, list position) but takes
// src's metadata where it is richer: a derived album ref has no Year, the
// song's artists (features included) and the song thumb, while the vertical's
// record carries the real album artists, year and cover.
func enrichAlbum(dst, src model.Album) model.Album {
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if len(src.Artists) > 0 {
		dst.Artists = src.Artists
	}
	if src.Year != "" {
		dst.Year = src.Year
	}
	if src.ThumbURL != "" {
		dst.ThumbURL = src.ThumbURL
	}
	return dst
}

// ensureSearchFrame returns the search-results frame for the current search
// generation, pushing a fresh one (and focusing the main view) the first time a
// result for this generation arrives. Songs and albums arrive separately and
// share a generation, so the second result updates the same frame — which may
// no longer be on top (e.g. the user already opened an album view from the
// section that arrived first), so the whole stack is searched rather than just
// the top; a late result must never push a duplicate frame or steal focus.
func (m *Model) ensureSearchFrame(query string) *mainContent {
	for i := range m.stack {
		if fr := &m.stack[i]; fr.kind == mainSearch && fr.searchGen == m.searchGen {
			return fr
		}
	}
	m.stack = append(m.stack, mainContent{
		kind:      mainSearch,
		title:     "Search: \"" + query + "\"",
		searchGen: m.searchGen,
	})
	m.setFocus(focusMain)
	return &m.stack[len(m.stack)-1]
}

// pushAlbum pushes an album detail view onto the main-view stack. esc pops back
// to the search results with the prior cursor intact. Pushing also invalidates
// any in-flight library load, mirroring setMain and esc: 'o' is reachable while
// a Liked-Songs/playlist fetch is pending (e.g. from a queue row), and that
// trailing libTracksMsg must not setMain over the album view the user just
// opened. (The inverse ordering is already guarded — setMain bumps albumGen.)
func (m *Model) pushAlbum(a model.Album, tracks []model.Track) {
	m.libGen++
	m.stack = append(m.stack, mainContent{
		kind:   mainAlbum,
		title:  a.Title,
		album:  a,
		tracks: tracks,
	})
}

// topAlbum returns the album of the top frame when it is an album view.
func (m *Model) topAlbum() (model.Album, bool) {
	if len(m.stack) == 0 {
		return model.Album{}, false
	}
	top := m.stack[len(m.stack)-1]
	if top.kind != mainAlbum {
		return model.Album{}, false
	}
	return top.album, true
}

// ensureAlbumCover makes sure the album's cover is (being) rendered for the
// current theme. It prefers a cached render, then a local re-render of an
// already-decoded image, otherwise a one-shot download (deduped per browseID).
// The view falls back to a placeholder until a render is available.
func (m *Model) ensureAlbumCover(a model.Album) tea.Cmd {
	key := m.artKey(a.BrowseID)
	if _, ok := m.albumArtCache[key]; ok {
		return nil
	}
	if img, ok := m.albumImgCache[a.BrowseID]; ok {
		m.albumArtCache[key] = art.Render(img, panels.AlbumCoverCols, panels.AlbumCoverRows, m.artOptions())
		return nil
	}
	if a.ThumbURL == "" {
		return nil
	}
	if _, busy := m.albumArtInflight[a.BrowseID]; busy {
		return nil
	}
	m.albumArtInflight[a.BrowseID] = struct{}{}
	return albumCoverCmd(a.BrowseID, a.ThumbURL)
}

// refreshAlbumCover re-renders the open album view's cover for the current theme
// from the decoded-image cache (no download). A cache miss leaves the
// placeholder until the in-flight fetch (keyed by browseID) completes.
func (m *Model) refreshAlbumCover() {
	a, ok := m.topAlbum()
	if !ok {
		return
	}
	key := m.artKey(a.BrowseID)
	if _, ok := m.albumArtCache[key]; ok {
		return
	}
	if img, ok := m.albumImgCache[a.BrowseID]; ok {
		m.albumArtCache[key] = art.Render(img, panels.AlbumCoverCols, panels.AlbumCoverRows, m.artOptions())
	}
}

// albumCoverBlock returns the rendered cover for the album view, or a procedural
// placeholder while the real cover is loading.
func (m Model) albumCoverBlock(a model.Album) string {
	if s, ok := m.albumArtCache[m.artKey(a.BrowseID)]; ok {
		return s
	}
	return art.Placeholder(a.BrowseID, panels.AlbumCoverCols, panels.AlbumCoverRows, m.artOptions())
}

func (m *Model) setStatus(s string) { m.status, m.statusErr = s, false }
func (m *Model) setError(s string)  { m.status, m.statusErr = s, true }

// canLoadLibrary reports whether real library/playlist browses should run: a
// library source is configured (OAuth Data API client, or a cookie InnerTube
// client). With no source (a nil interface) the UI falls back to mock data plus
// a sign-in hint. A configured-but-stale source still attempts the browse and
// degrades on ErrNotSignedIn (see the libPlaylistsMsg/libTracksMsg handlers).
func (m Model) canLoadLibrary() bool {
	return m.lib != nil
}

// downgradeAuth flips the resolved sign-in state to anonymous: a library browse
// just came back as the logged-out page, so the cookie rotated mid-session
// (Google does this within hours — see README). The header indicator switches
// to the stale-cookie hint and library actions stop re-issuing doomed browses,
// keeping the indicator and the library behavior consistent.
func (m *Model) downgradeAuth() {
	m.authChecked = true
	m.authSignedIn = false
	m.authName = ""
}

// loginHint is the status shown when the OAuth session is revoked: every
// request will keep failing the same way until the user signs in again.
const loginHint = "sign-in expired — run tubeamp -login"

// invalidGrant reports whether err's chain carries a Google OAuth
// "invalid_grant" error (a revoked or expired refresh token), matched with
// errors.As so the ytm wrapping ("ytm: refresh oauth token: …") is transparent.
func invalidGrant(err error) bool {
	var oerr *ytm.OAuthError
	return errors.As(err, &oerr) && oerr.Code == "invalid_grant"
}

// requestErrText renders a request failure for the status line, replacing the
// raw error text with the re-login prompt when the OAuth session is revoked —
// a generic "could not load …: oauth error" gives the user no remedy.
func requestErrText(prefix string, err error) string {
	if invalidGrant(err) {
		return loginHint
	}
	return prefix + err.Error()
}

// authIndicator returns the styled sign-in status shown right-aligned on the
// wordmark header row (or the status line when the header is hidden on the
// shortest terminal), or "" before the one-shot AccountInfo check has resolved.
// Signed in => "● <name>" in PlayingStyle; credentials that resolve anonymous =>
// "○ anonymous — cookie stale? see README" in Muted; no credentials at all =>
// "○ not signed in" in Muted.
func (m Model) authIndicator() string {
	if !m.authChecked {
		return ""
	}
	if m.authSignedIn {
		name := m.authName
		if name == "" {
			name = "signed in"
		}
		return m.th.PlayingStyle().Render("● " + name)
	}
	if m.hasAuth {
		return m.th.Muted().Render("○ anonymous — cookie stale? see README")
	}
	return m.th.Muted().Render("○ not signed in")
}

// artistLabels formats the Library "Artists" rows as "♪ <name> (<n>)", where n
// is how many of the user's library tracks are attributed to that artist.
func artistLabels(groups []ytdata.ArtistGroup) []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = fmt.Sprintf("♪ %s (%d)", g.Name, len(g.Tracks))
	}
	return out
}

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

	// Wordmark header: one row (logoHeight), shown whenever the terminal has the
	// headroom (>= logoMinTermHeight); at the very shortest height it is
	// suppressed and the panel area reclaims the row. The accent-coloured
	// "tubeamp" wordmark sits top-left; the version tag and the sign-in indicator
	// ride the same row, right-aligned. When the header is hidden the indicator
	// falls through to the status line.
	ind := m.authIndicator()
	logo := ""
	if m.height >= logoMinTermHeight {
		logo = renderLogo(m.th, m.width, ind)
	}

	leftW := clamp(m.width*3/10, 24, 40)
	mainW := m.width - leftW
	// player bar (6) + hint line (1) + header row when shown
	topH := m.topAreaHeight()

	// The lyrics panel (when shown) takes a band at the bottom of the RIGHT
	// column; the main view shrinks to fill the rest. The left column is
	// unchanged, so the top area still totals topH rows. Hidden when idle or too
	// short — then mainH == topH and the layout is exactly as before.
	lyricsH := m.lyricsBandHeight(topH)
	mainH := topH - lyricsH

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
	mainFocused := m.focus == focusMain
	var mainBox string
	switch top.kind {
	case mainAlbum:
		cover := m.albumCoverBlock(top.album)
		mainBox = panels.AlbumView(m.th, "4 "+top.title, top.album, top.tracks, top.cursor, cover, playingID, mainW, mainH, mainFocused)
	case mainSearch:
		mainBox = panels.SearchView(m.th, "4 "+top.title, top.tracks, top.albums, top.cursor, playingID, mainW, mainH, mainFocused)
	case mainArtists:
		mainBox = panels.ArtistListView(m.th, "4 "+top.title, artistLabels(top.groups), top.cursor, mainW, mainH, mainFocused)
	case mainAlbums:
		mainBox = panels.AlbumListView(m.th, "4 "+top.title, top.albums, top.cursor, mainW, mainH, mainFocused)
	default:
		mainBox = panels.MainView(m.th, "4 "+top.title, top.tracks, top.cursor, playingID, mainW, mainH, mainFocused)
	}

	// The lyrics panel rides below the main view, above the player bar, within the
	// right column. It draws with the active border when it is the focused panel;
	// while focused its scroll/detached state drives what it shows (a focused
	// peek-scroll detaches synced auto-follow). Unfocused it renders from the top
	// (unsynced) / the live line (synced), exactly as a pure-display panel.
	rightCol := mainBox
	if lyricsH > 0 {
		st, lines, plain, cur := m.lyricsForView()
		active := m.focus == focusLyrics
		scroll, detached := 0, false
		if active {
			scroll, detached = m.lyricsScroll, m.lyricsDetached
		}
		lyricsBox := panels.LyricsView(m.th, st, lines, plain, cur, scroll, detached, active, mainW, lyricsH)
		rightCol = lipgloss.JoinVertical(lipgloss.Left, mainBox, lyricsBox)
	}

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol)
	bar := panels.PlayerBar(m.th, m.playerState(), m.artBlock, m.width)
	hint := m.statusRow(logo != "", ind)

	// Assemble: wordmark header then panels, player bar, hints.
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

// statusRow renders the bottom status/hint line. The sign-in indicator normally
// rides the wordmark header row; only when the header is hidden (the very
// shortest terminal) does it fall through onto this line, right-aligned.
func (m Model) statusRow(logoShown bool, ind string) string {
	if logoShown || ind == "" {
		return m.bottomLine(m.width)
	}
	indW := lipgloss.Width(ind)
	// Like the header row, a long account name must clip (ANSI-aware, ellipsis
	// tail), never widen the row: an oversized indicator here would make
	// JoinVertical pad EVERY frame row past the terminal width, hard-wrapping and
	// garbling the whole UI.
	if avail := m.width - 1; indW > avail {
		ind = ansi.Truncate(ind, avail, "…")
		indW = lipgloss.Width(ind)
	}
	left := m.bottomLine(m.width - indW - 1)
	gap := m.width - lipgloss.Width(left) - indW
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + ind
}

// bottomLine renders the single-line status/hint bar, clipped to maxW columns.
// A transient status toast takes precedence over the context hints.
func (m Model) bottomLine(maxW int) string {
	if maxW < 0 {
		maxW = 0
	}
	if m.status != "" {
		st := m.th.AccentStyle()
		if m.statusErr {
			st = m.th.ErrorStyle()
		}
		return st.Render(panels.Clip(" "+m.status, maxW))
	}
	var parts []string
	for _, h := range m.contextHints() {
		parts = append(parts, h.key+" "+h.desc)
	}
	line := " " + strings.Join(parts, "  ·  ")
	return m.th.Muted().Render(panels.Clip(line, maxW))
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
		return []hint{{"j/k", "preview"}, {k.Enter.Help().Key, "keep"}, {k.Esc.Help().Key, "revert"}}
	}
	switch m.focus {
	case focusLibrary, focusPlaylists:
		return []hint{{"j/k", "move"}, {k.Enter.Help().Key, "open"}, {"1-4/hl", "focus"},
			{k.Search.Help().Key, "search"}, {k.Theme.Help().Key, "theme"}, {k.Help.Help().Key, "help"}}
	case focusQueue:
		return []hint{{"j/k", "move"}, {k.Enter.Help().Key, "play"}, {k.Open.Help().Key, "open album"},
			{k.Remove.Help().Key, "remove"}, {"J/K", "reorder"}, {k.ClearQueue.Help().Key, "clear"}}
	case focusLyrics:
		// Synced lyrics peek-scroll (following vs detached); unsynced plain
		// scroll. While loading / no lyrics the scroll keys are no-ops
		// (scrollLyrics returns early), so advertise only keys that do something.
		r, ok := m.currentLyric()
		if !ok || r.status != lyricResolved || !r.found {
			return []hint{{"h/l", "focus"}, {k.Help.Help().Key, "help"}}
		}
		if r.ly.Synced {
			if m.lyricsDetached {
				return []hint{{"⏸", "paused"}, {k.Esc.Help().Key, "follow"}}
			}
			return []hint{{"▶", "following"}, {"j/k", "peek"}}
		}
		return []hint{{"j/k", "scroll"}, {"ctrl+u/d", "page"}}
	default: // focusMain
		top := m.stack[len(m.stack)-1]
		switch {
		case top.kind == mainAlbum:
			return []hint{{"j/k", "move"}, {k.Enter.Help().Key, "play from here"},
				{k.Esc.Help().Key, "back"}, {k.Search.Help().Key, "search"}, {k.Help.Help().Key, "help"}}
		case top.kind == mainArtists:
			return []hint{{"j/k", "move"}, {k.Enter.Help().Key, "show tracks"},
				{"1-4/hl", "focus"}, {k.Search.Help().Key, "search"}, {k.Help.Help().Key, "help"}}
		case (top.kind == mainSearch || top.kind == mainAlbums) && top.cursor >= len(top.tracks):
			// An album row is selected.
			return []hint{{"j/k", "move"}, {k.Enter.Help().Key, "play album"},
				{k.Open.Help().Key, "open album"}, {k.Search.Help().Key, "search"}, {k.Help.Help().Key, "help"}}
		default:
			return []hint{{"j/k", "move"}, {k.Enter.Help().Key, "play"}, {k.Open.Help().Key, "open album"},
				{k.Append.Help().Key, "queue"}, {k.InsertNext.Help().Key, "play next"}, {k.Search.Help().Key, "search"}}
		}
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
