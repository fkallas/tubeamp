# tubeamp — package contracts

tubeamp is a Lazygit-inspired terminal UI for YouTube Music. One Go binary:
Bubble Tea TUI on top, mpv (spawned subprocess, JSON IPC over a unix socket)
for audio, YT Music's private InnerTube API for metadata. mpv resolves
`music.youtube.com/watch?v=...` URLs itself via its yt-dlp hook — we never
touch raw stream URLs.

This file is the **single source of truth for public APIs**. Implement your
package's exported surface *exactly* as written here (names, signatures,
semantics). Internal design is yours. If you believe a contract is wrong, note
it in your report — do not silently deviate.

## Ground rules

- Module: `github.com/fkallas/tubeamp`. Go 1.22+.
- Dependencies are already in `go.mod`/`go.sum`. **Never edit `go.mod`,
  `go.sum`, or run `go get` / `go mod`** — parallel writers would conflict.
- Allowed deps: stdlib, `github.com/charmbracelet/bubbletea` (**v1 API**:
  `Init() tea.Cmd`, `Update(tea.Msg) (tea.Model, tea.Cmd)`, `View() string`),
  `github.com/charmbracelet/lipgloss`, `github.com/charmbracelet/bubbles/key`,
  `github.com/charmbracelet/bubbles/textinput`, `gopkg.in/yaml.v3`.
- Compile and test your own package before finishing:
  `go build ./internal/<pkg>/...` and `go test ./internal/<pkg>/...`.
  Run `gofmt -w` on your files.
- Tests must not touch the real `$HOME` or network (use `t.TempDir()`,
  `t.Setenv`). One optional, clearly-marked live test may be skipped by
  default. Tests asserting rendered output must not depend on ANSI codes
  (color profile is terminal-dependent); assert structure/content.
- Errors: wrap with `fmt.Errorf("...: %w", err)`. Doc comments on all
  exported identifiers. No `panic` in library code.
- Unix only for now (darwin/linux).

## internal/model (already written — do not modify)

```go
type Track struct {
    VideoID  string
    Title    string
    Artists  []string
    Album    string
    Duration time.Duration
    ThumbURL string
}
func (t Track) ArtistLine() string // "A, B"
func (t Track) URL() string        // https://music.youtube.com/watch?v=<id>

type Playlist struct{ ID, Title string; TrackCount int }
type Album struct{ BrowseID, Title string; Artists []string; Year, ThumbURL string }
type Artist struct{ BrowseID, Name, ThumbURL string }
```

## internal/config

```go
type Config struct {
    Theme      string `yaml:"theme"`       // default "catppuccin-mocha"
    Volume     int    `yaml:"volume"`      // 0-100, default 80
    MPVPath    string `yaml:"mpv_path"`    // default "mpv" (PATH lookup)
    YTDLFormat string `yaml:"ytdl_format"` // default "bestaudio"
}

func Default() *Config
func Load() (*Config, error)      // ConfigDir()/config.yaml; missing file => Default(), nil error.
                                  // Unset fields fall back to defaults.
func (c *Config) Save() error     // writes ConfigDir()/config.yaml, creating dirs (0755)

func ConfigDir() string  // $XDG_CONFIG_HOME/tubeamp  or ~/.config/tubeamp
func CacheDir() string   // $XDG_CACHE_HOME/tubeamp   or ~/.cache/tubeamp
func DataDir() string    // $XDG_DATA_HOME/tubeamp    or ~/.local/share/tubeamp
func ThemesDir() string  // ConfigDir()/themes
```

## internal/store

```go
// Cache is a tiny file-backed byte cache (filenames = hash of key).
type Cache struct{ ... }
func NewCache(dir string) *Cache                                  // creates dir lazily
func (c *Cache) Get(key string, maxAge time.Duration) ([]byte, bool) // miss if absent/expired/maxAge<=0-never-expires? No: maxAge<=0 means no expiry
func (c *Cache) Put(key string, data []byte) error
```

## internal/core

`Queue` is the UI's **mirror of daemon state**, not the playback authority: the
mpv playlist is. The UI rebuilds it from `player.Snapshot()` on attach and
updates the current index from `EvPlaylistPos`; mutations are applied locally for
instant feedback and routed to the daemon via the `player.Playlist*`/`Next`/`Prev`
methods.

```go
// Queue is the play queue (UI mirror). Not goroutine-safe; owned by the UI loop.
type Queue struct{ ... }
func NewQueue() *Queue
func (q *Queue) Items() []model.Track
func (q *Queue) Len() int
func (q *Queue) Index() int                       // -1 when nothing current
func (q *Queue) Current() (model.Track, bool)
func (q *Queue) Set(ts []model.Track, start int)  // replace queue, current = start
func (q *Queue) SetIndex(i int)                    // set current directly, clamped to [-1, len-1] (reconcile EvPlaylistPos; no command)
func (q *Queue) Append(ts ...model.Track)
func (q *Queue) InsertNext(ts ...model.Track)     // after current
func (q *Queue) Remove(i int)                     // removing current keeps index pointing at next item
func (q *Queue) Move(i, j int)
func (q *Queue) JumpTo(i int) (model.Track, bool) // sets current
func (q *Queue) Advance() (model.Track, bool)     // false at end (index stays)
func (q *Queue) Prev() (model.Track, bool)
func (q *Queue) Clear()
```

## internal/theme

```go
type Colors struct { // all hex strings like "#fabd2f"
    BorderInactive string `yaml:"border_inactive"`
    BorderActive   string `yaml:"border_active"`
    TextPrimary    string `yaml:"text_primary"`
    TextMuted      string `yaml:"text_muted"`
    Accent         string `yaml:"accent"`
    Playing        string `yaml:"playing"`
    Error          string `yaml:"error"`
    SelectionBG    string `yaml:"selection_bg"`
    SelectionFG    string `yaml:"selection_fg"`
}
type Theme struct {
    Name   string `yaml:"name"`
    Colors Colors `yaml:"colors"`
}

func Default() *Theme                            // catppuccin-mocha
func Builtins() []string                         // sorted builtin names
func List(userDir string) []string               // builtins + user themes, sorted, deduped
func Load(name, userDir string) (*Theme, error)  // userDir/<name>.yaml first, then builtin;
                                                 // error lists available names

// Lipgloss helpers (no other package hardcodes colors — ever):
func (t *Theme) PanelBorder(active bool) lipgloss.Style // rounded border, BorderActive/Inactive
func (t *Theme) Title(active bool) lipgloss.Style       // panel titles
func (t *Theme) Primary() lipgloss.Style
func (t *Theme) Muted() lipgloss.Style
func (t *Theme) AccentStyle() lipgloss.Style
func (t *Theme) PlayingStyle() lipgloss.Style
func (t *Theme) ErrorStyle() lipgloss.Style
func (t *Theme) Selected() lipgloss.Style               // SelectionFG on SelectionBG
func (t *Theme) Palette() []color.Color                 // all theme colors, for art quantization
```

Builtins (embed YAML via `go:embed builtin/*.yaml`, canonical palettes):
`catppuccin-mocha`, `catppuccin-latte`, `gruvbox-dark`, `nord`, `dracula`,
`tokyo-night`. User themes are the same YAML shape.

## internal/art

Half-block pixel-art renderer. Each terminal cell is two vertical pixels via
`▀` (fg = top pixel, bg = bottom pixel), so cols×rows cells = cols×(2·rows) px.

```go
type Options struct {
    PaletteSize int           // >0: median-cut quantize to N colors
    Palette     []color.Color // non-nil: snap to this fixed palette (theme art!)
    Dither      bool          // Bayer 4x4 ordered dithering
}

// Render center-crops img to the target pixel aspect (cols : 2*rows), scales
// nearest-neighbor, applies Options, and returns rows lines of lipgloss-styled
// half-blocks.
func Render(img image.Image, cols, rows int, o Options) string

// Placeholder is a deterministic procedural cover for tracks without art:
// a left-right-mirrored sprite (like a tiny space invader) seeded by `seed`,
// colored from o.Palette when given.
func Placeholder(seed string, cols, rows int, o Options) string

// RewriteThumbURL rewrites googleusercontent/ggpht URLs to request px×px
// (replace trailing =w###-h###... / =s### segment). Other URLs pass through.
func RewriteThumbURL(raw string, px int) string

func Fetch(ctx context.Context, url string) (image.Image, error) // jpeg/png/webp-as-jpeg via stdlib decoders (jpeg+png only is fine)
```

## internal/player

Client for a **persistent, detached mpv daemon** that owns the playback
playlist. mpv is spawned once — `setsid`, stdio to `/dev/null`, the process
`Release`d so the parent never `Wait`s or kills it — and keeps playing after the
TUI exits. Spawn flags: `mpv --idle=yes --no-video --no-terminal
--input-ipc-server=<sock> --volume=<n> --ytdl-format=<f> --prefetch-playlist=yes
--gapless-audio=weak`. The last two give **gapless prefetch**: mpv resolves +
opens the next playlist entry (via yt-dlp) shortly before the current one ends
and crossfeeds without re-initialising the audio chain when codecs match.

The socket is fixed at `config.DataDir()/mpv.sock`. `New` first tries to ATTACH
to an existing socket (connect OK → no spawn); otherwise it SPAWNS, guarded by an
`O_CREATE|O_EXCL` lock file (`<sock>.lock`) plus an attach-retry loop so two
concurrent clients never start two daemons. The lock records the daemon's pid:
a lock whose owner is dead (crashed daemon) is reclaimed immediately; a lock
with a live owner is never stolen. The mpv playlist **is** the queue;
the ordered, richly-typed track list is mirrored to `config.DataDir()/queue.json`
(atomic temp+rename) on every mutation so a re-attaching client can rebuild full
metadata (mpv itself only remembers titles for entries it has already played).

```go
type Options struct {
    MPVPath    string // "" => "mpv"
    SocketPath string // "" => config.DataDir()/mpv.sock
    YTDLFormat string // "" => "bestaudio"
    Volume     int    // initial volume (spawn only)
    AttachOnly bool   // never spawn; ErrNotRunning when no daemon is listening
}

var ErrNotRunning = errors.New("player: mpv daemon not running") // New(AttachOnly) with no daemon

type EventKind int
const (
    EvTimePos  EventKind = iota // Float seconds
    EvDuration                  // Float seconds
    EvPause                     // Bool
    EvVolume                    // Float 0-100
    EvMute                      // Bool
    EvFileLoaded                // new track started
    EvTrackEnded                // end-file with reason "eof" (UI no longer advances on this)
    EvError                     // Str message (incl. end-file reason "error")
    EvPlaylistPos               // Int: current playlist index, -1 when idle
)
type Event struct {
    Kind  EventKind
    Float float64
    Bool  bool
    Str   string
    Int   int // EvPlaylistPos
}

// Snapshot is the full daemon state a re-attaching client rebuilds from.
type Snapshot struct {
    Tracks      []model.Track
    PlaylistPos int     // current playing index, -1 when idle
    Paused      bool
    TimePos     float64 // seconds
    Duration    float64 // seconds
    Volume      int
    Mute        bool
}

type Player struct{ ... }
func New(o Options) (*Player, error)   // attach-or-spawn; AttachOnly => ErrNotRunning if no daemon
func (p *Player) Events() <-chan Event // buffered (~64); sender drops oldest-style (non-blocking) rather than stall
func (p *Player) Load(url string) error // loadfile <url> replace (single-file; does not touch the sidecar)
func (p *Player) TogglePause() error
func (p *Player) Stop() error                  // mpv's stop also clears the playlist; the tracks
                                               // mirror and queue.json sidecar are reset to match
func (p *Player) Seek(offsetSec float64) error // relative
func (p *Player) SetVolume(pct int) error      // clamp 0..120
func (p *Player) ToggleMute() error

// Playlist-backed queue. Each entry loads via ["loadfile", url, mode, -1, {force-media-title:<title>}]
// (mpv >= 0.38 form) so bare mpv consumers see names. mpv auto-advances; the
// current index is reported via observe_property playlist-pos (-1 only when truly
// idle; playlist-playing-pos is deliberately NOT observed — it emits a transient
// -1 between tracks on every auto-advance). PlaylistReplace stops (clearing the
// old playlist), appends all entries while idle, then sets playlist-pos=start,
// so entry 0 is never transiently loaded/reported when start > 0.
func (p *Player) PlaylistReplace(ts []model.Track, start int) error
func (p *Player) PlaylistAppend(ts ...model.Track) error
func (p *Player) PlaylistRemove(i int) error
func (p *Player) PlaylistMove(i, j int) error  // mpv playlist-move semantics (i<j lands at j-1)
func (p *Player) PlaylistJump(i int) error
func (p *Player) Next() error                  // playlist-next weak (no-op at end)
func (p *Player) Prev() error                  // playlist-prev weak (no-op at start)
func (p *Player) PlaylistClear() error

func (p *Player) Snapshot() (Snapshot, error)  // queue.json reconciled entry-by-entry (URL match) with the live
                                               // playlist + transport props; mismatched/missing sidecar entries
                                               // degrade to titles from the mpv playlist

func (p *Player) Close() error // DETACH: stop goroutines, fail pending, close Events, close conn — mpv, socket
                               // and playback are left untouched (this is what makes playback persist). Idempotent.
func (p *Player) Quit() error  // terminate the daemon: send mpv quit, detach, verify the process exits
                               // (escalating to SIGTERM/SIGKILL), remove socket + lock files (-kill)
```

## internal/ytm (+ internal/ytm/parse)

InnerTube (YT Music private API) client skeleton. POST JSON to
`https://music.youtube.com/youtubei/v1/<endpoint>?prettyPrint=false` with a
`context.client` of `{"clientName":"WEB_REMIX","clientVersion":"1.20240101.01.00"}`
(no API key needed). Authenticated requests add the `Cookie` header plus
`Authorization: SAPISIDHASH <ts>_<sha1hex(ts + " " + SAPISID + " " + origin)>`
and `X-Origin`/`Origin: https://music.youtube.com`.

```go
// package ytm
type Auth struct{ Cookie string } // raw Cookie header value
func LoadAuth(path string) (*Auth, error) // plain-text file, one line; DataDir()/auth by convention
func (a *Auth) SAPISID() (string, error)  // parsed from cookie (SAPISID or __Secure-3PAPISID)

type Client struct{ ... }
func NewClient(a *Auth) *Client // a may be nil => unauthenticated (search still works)
func (c *Client) Search(ctx context.Context, query string) ([]model.Track, error)       // songs filter
func (c *Client) SearchAlbums(ctx context.Context, query string) ([]model.Album, error) // albums filter
func (c *Client) GetAlbum(ctx context.Context, browseID string) (model.Album, []model.Track, error)
                                                  // browse endpoint {browseId: ...}; sets Album.BrowseID = browseID

// package ytm/parse — ALL response JSON parsing lives here, fixture-tested.
func SearchTracks(raw []byte) ([]model.Track, error)                  // songs search shelf
func SearchAlbums(raw []byte) ([]model.Album, error)                  // albums search shelf + top-result card
func AlbumPage(raw []byte) (model.Album, []model.Track, error)        // album header + track shelf
// All defensive: skip malformed items, never panic. AlbumPage handles BOTH
// header shapes (musicDetailHeaderRenderer and musicResponsiveHeaderRenderer);
// per-track artists fall back to album artists, Track.Album = album title,
// Track.ThumbURL = album thumb. The returned Album has no BrowseID (caller sets it).
```

Response shapes change under us; parsers must tolerate missing keys. Mark the
songs-/albums-filter `params` constants with a `// TODO: verify against ytmusicapi`
comment. Parser fixtures in `parse/testdata/` (`search_albums.json`,
`album_page.json`) are trimmed live captures; `album_page_detail.json` is
handcrafted to exercise the older `musicDetailHeaderRenderer` shape.

## internal/ui (+ internal/ui/panels, internal/ui/overlay, internal/ui/keymap)

```go
// package ui
func New(cfg *config.Config, th *theme.Theme, p *player.Player, c *ytm.Client, q *core.Queue) Model
// Model implements tea.Model (v1). Run with tea.NewProgram(m, tea.WithAltScreen()).
// p and c may each be nil => degraded mode (status-line notice instead of crash).

// ANSI-aware overlay compositor (compose.go). Splices an overlay box over the
// fully-rendered app view WITHOUT blanking the rows it sits on: for each overlay
// row it keeps background columns [0,x), drops in the overlay row, then keeps
// background columns [x+w,…). SGR state on both sides is cut/restored so styles
// never bleed across the seams; a wide rune bisected by a seam becomes a space.
// Overlays wider/taller than the background are clamped; a row shorter than x is
// space-padded. View composites help/search/theme overlays via CompositeCenter.
func Composite(overlay, background string, x, y int) string
func CompositeCenter(overlay, background string) string
```

### Layout

```
╭◉╮ tubeamp                                          v0.1.0   ← logo header (2 rows)
╰─╯ ─────────────────────────
╭─1 Library──╮╭─4 <context title> ─────────────╮
│            ││                                 │
╰────────────╯│                                 │
╭─2 Playlists╮│      main view                  │
│            ││                                 │
╰────────────╯│                                 │
╭─3 Queue────╮│                                 │
│            ││                                 │
╰────────────╯╰─────────────────────────────────╯
╭─ player bar (art 8×4 cells │ title/artist/album │ progress, vol) ─╮
╰───────────────────────────────────────────────────────────────────╯
 <context-sensitive key hints, single line>
```

Logo header: 2 rows, at most ~28 cols for the glyph+wordmark. The glyph
("╭◉╮") is rendered in AccentStyle, the wordmark "tubeamp" in Primary, and
"v0.1.0" right-aligned in Muted. When terminal height < 24 the logo is hidden
entirely and the panel area reclaims those 2 rows.

Left column ~30% width (min 24, max 40 cols). Library panel fixed-height
(items + border), Playlists/Queue split the rest. Player bar 4 content lines.
Focused panel gets BorderActive + active Title. Handle `tea.WindowSizeMsg`
everywhere; below ~70×20 show a centered "terminal too small" notice.

Main-view track table columns: TITLE, ARTIST, ALBUM (Muted), then duration
flushed right. The ALBUM column is dropped when the main view is narrower than
~80 cols; when space is tight columns truncate in priority order
TITLE > ARTIST > ALBUM. Queue rows append a Muted " — <album>" suffix only when
the Queue panel is ~34 cols or wider; otherwise the row is unchanged.

The main view is a stack of frames; a frame is one of three kinds:
- **track list** (library/playlist/single search-song play) — the table above.
- **search results** (`panels.SearchView`) — a Muted "Songs" header + the track
  table, then a Muted "Albums" header + album rows `▤ <Title> — <Artists> (<Year>)`.
  A single selection cursor runs through both sections (songs first, then albums)
  so `↑`/`↓` move through them seamlessly.
- **album view** (`panels.AlbumView`) — a header row with a large
  `panels.AlbumCoverCols`×`panels.AlbumCoverRows` (16×8) pixel-art cover on the
  left and the album title (Accent), artists, year + track count (Muted) to its
  right; below, the album's track list (number, title, duration — no album column).

### Keymap (package keymap, bubbles/key bindings; this is the spec reviewers check)

Global: `1` focus Library; `2` focus Playlists; `3` focus Queue; `4` focus Main
view; `←`/`→` seek -5s/+5s; `space` pause; `n`/`p` next/previous track;
`+`/`=`/`-` volume; `m` mute; `/` search overlay; `T` theme picker; `?` help
overlay; `q`/`ctrl+c` quit; `esc` closes overlay / pops view stack.

Per panel: `↑`/`↓` arrows move selection; `g/G` top/bottom; `enter` activates.
Library/Playlists `enter` → load (mock) tracks into main view. Main view
`enter` → `queue.Set(visibleTracks, cursor)` + play; `a` append to queue;
`A` insert-next; `o` open album (album rows only). Queue: `enter` jump-to-track,
`d` remove, `J/K` move item, `c` clear.

Album flows (main view): a search produces two sections — Songs then Albums (see
below). On an **album row**: `enter` fetches `GetAlbum` then `PlaylistReplace`s the
queue with the album from track 0 and plays ("Playing <album>" toast); `o` fetches
`GetAlbum` then pushes a dedicated **album view** onto the main-view stack. In the
**album view**: `↑`/`↓` move; `enter` = `PlaylistReplace(albumTracks, selected)` +
play (the whole album, starting at the selected track); `esc` pops back to the
search results with the cursor preserved. GetAlbum and the album-cover download
each carry a generation guard (like the search guard) so stale results are dropped;
nil-player / nil-client are handled with status-line notices, never a crash.

### Required patterns

- **Never block in `Update`.** All IO (player commands, HTTP, art fetch,
  search) via `tea.Cmd`.
- Player event bridge — one receive per Cmd, re-issue after each msg:

```go
func listenPlayer(p *player.Player) tea.Cmd {
    return func() tea.Msg {
        ev, ok := <-p.Events()
        if !ok { return playerClosedMsg{} }
        return playerEventMsg(ev)
    }
}
```

- mpv advances the playlist itself: the UI updates its current index from
  `EvPlaylistPos` (`Int`, -1 = end-of-queue idle), NOT from `EvTrackEnded` (that
  event is now a no-op for advancing). On attach with a live daemon, `New` calls
  `player.Snapshot()` synchronously so the player bar and queue panel show the
  in-progress track on the first render. Queue mutations (enter/`a`/`A`/`d`/`J`/`K`/`c`,
  `n`/`p`) update the local `core.Queue` for instant feedback and dispatch the
  matching `player.Playlist*`/`Next`/`Prev` call in a Cmd; the index reconciles on
  the next `EvPlaylistPos`.
- Art: player bar shows 8×4-cell cover. If `track.ThumbURL != ""` fetch via
  Cmd (`art.Fetch` + `art.Render` with theme palette options, cache by
  videoID+theme in the model); fallback/loading state `art.Placeholder`.
  Mock thumbs: `https://i.ytimg.com/vi/<videoID>/mqdefault.jpg`.
- Theme picker overlay: lists `theme.List(config.ThemesDir())`, live-previews
  on cursor move, `enter` = keep + `cfg.Save()` (via Cmd), `esc` = revert.
- Search overlay: `bubbles/textinput`; on enter, if ytm client non-nil run
  `Search` and `SearchAlbums` Cmds concurrently (`tea.Batch`, both 5s timeout
  ctx) → results become a search-results frame (Songs + Albums sections); render
  whichever returns first, fill the other section on arrival; on error or nil
  client, status-line message.
- Album cover (album view): fetch via Cmd from `RewriteThumbURL(thumb, 64)`,
  render 16×8 with theme palette options, `Placeholder` while loading, cache by
  browseID+theme (mirrors the player-bar art caching).
- Status line doubles as transient error/info toast (e.g. "mpv not found —
  playback disabled").
- Use `lipgloss.Width`/`Height` for layout math on styled strings, never `len()`.

Mock data lives in `internal/ui/mock.go` (provided in task prompt) until the
real library lands.

## cmd/tubeamp

`main.go`: parse flags; `config.Load`. With no control flag set it runs the TUI:
`-theme <name>` override, `-version`; `theme.Load` (fall back to `theme.Default()`
with a warning); `player.New` (attach-or-spawn; on error nil player + degraded
notice); `ytm.LoadAuth(DataDir()/auth)` (missing → nil auth) + `ytm.NewClient`;
`core.NewQueue`; `ui.New`; `tea.NewProgram(..., tea.WithAltScreen())`. On exit:
`player.Close()` — a DETACH, so mpv keeps playing in the background.

`control.go`: when any one-shot control flag is set, `dispatchControl` drives the
running daemon instead of opening the TUI. All use `player.New` with
`AttachOnly`; when no daemon is running they print `tubeamp: not running` to
stderr and exit 1 — **except `-line`, which stays silent and exits 0** so it can
feed tmux/status scripts.

| Flag        | Action                                                                 |
|-------------|------------------------------------------------------------------------|
| `-p`        | toggle pause                                                           |
| `-next`     | skip to next track (`playlist-next`)                                   |
| `-prev`     | skip to previous track (`playlist-prev`)                               |
| `-stop`     | stop playback                                                         |
| `-vol N`    | set volume: `N` absolute, `+N`/`-N` relative                          |
| `-seek S`   | seek `±S` seconds (relative)                                          |
| `-status`   | multi-line human-readable status (glyph, title, artists, album, pos/dur, volume, track n/m) |
| `-line`     | one compact line `♪ Title — Artist 1:23/3:54` (~48 cols); empty when idle or no daemon |
| `-queue`    | numbered queue, playing row marked `▶`                               |
| `-kill`     | `player.Quit()` the daemon (removes socket + lock)                    |
