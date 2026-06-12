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

```go
// Queue is the play queue. Not goroutine-safe; owned by the UI loop.
type Queue struct{ ... }
func NewQueue() *Queue
func (q *Queue) Items() []model.Track
func (q *Queue) Len() int
func (q *Queue) Index() int                       // -1 when nothing current
func (q *Queue) Current() (model.Track, bool)
func (q *Queue) Set(ts []model.Track, start int)  // replace queue, current = start
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

mpv wrapper. Spawn `mpv --idle=yes --no-video --no-terminal
--input-ipc-server=<sock> --volume=<n> --ytdl-format=<f>`; connect with retry
(~5s budget); speak the JSON-lines IPC protocol with request_id correlation;
`observe_property` for time-pos, duration, pause, volume, mute.

```go
type Options struct {
    MPVPath    string // "" => "mpv"
    SocketPath string // "" => os.TempDir()/tubeamp-mpv-<pid>.sock
    YTDLFormat string // "" => "bestaudio"
    Volume     int    // initial volume
}

type EventKind int
const (
    EvTimePos  EventKind = iota // Float seconds
    EvDuration                  // Float seconds
    EvPause                     // Bool
    EvVolume                    // Float 0-100
    EvMute                      // Bool
    EvFileLoaded                // new track started
    EvTrackEnded                // end-file with reason "eof"
    EvError                     // Str message (incl. end-file reason "error")
)
type Event struct {
    Kind  EventKind
    Float float64
    Bool  bool
    Str   string
}

type Player struct{ ... }
func New(o Options) (*Player, error)   // error if binary missing / IPC never connects
func (p *Player) Events() <-chan Event // buffered (~64); sender drops oldest-style (non-blocking) rather than stall
func (p *Player) Load(url string) error // loadfile <url> replace
func (p *Player) TogglePause() error
func (p *Player) Stop() error
func (p *Player) Seek(offsetSec float64) error // relative
func (p *Player) SetVolume(pct int) error      // clamp 0..120
func (p *Player) ToggleMute() error
func (p *Player) Close() error // quit mpv (kill after timeout), close Events, remove socket; idempotent
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
func (c *Client) Search(ctx context.Context, query string) ([]model.Track, error) // songs filter

// package ytm/parse — ALL response JSON parsing lives here, fixture-tested.
func SearchTracks(raw []byte) ([]model.Track, error) // defensive: skip malformed items, never panic
```

Response shapes change under us; parsers must tolerate missing keys. Mark the
songs-filter `params` constant with a `// TODO: verify against ytmusicapi`
comment.

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

### Keymap (package keymap, bubbles/key bindings; this is the spec reviewers check)

Global: `1` focus Library; `2` focus Playlists; `3` focus Queue; `4` focus Main
view; `←`/`→` seek -5s/+5s; `space` pause; `n`/`p` next/previous track;
`+`/`=`/`-` volume; `m` mute; `/` search overlay; `T` theme picker; `?` help
overlay; `q`/`ctrl+c` quit; `esc` closes overlay / pops view stack.

Per panel: `↑`/`↓` arrows move selection; `g/G` top/bottom; `enter` activates.
Library/Playlists `enter` → load (mock) tracks into main view. Main view
`enter` → `queue.Set(visibleTracks, cursor)` + play; `a` append to queue;
`A` insert-next. Queue: `enter` jump-to-track, `d` remove, `J/K` move item,
`c` clear.

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

- `EvTrackEnded` → `queue.Advance()`; if ok, play next; else idle state.
- Art: player bar shows 8×4-cell cover. If `track.ThumbURL != ""` fetch via
  Cmd (`art.Fetch` + `art.Render` with theme palette options, cache by
  videoID+theme in the model); fallback/loading state `art.Placeholder`.
  Mock thumbs: `https://i.ytimg.com/vi/<videoID>/mqdefault.jpg`.
- Theme picker overlay: lists `theme.List(config.ThemesDir())`, live-previews
  on cursor move, `enter` = keep + `cfg.Save()` (via Cmd), `esc` = revert.
- Search overlay: `bubbles/textinput`; on enter, if ytm client non-nil run
  `Search` Cmd (5s timeout ctx) → results become main view content; on error
  or nil client, status-line message.
- Status line doubles as transient error/info toast (e.g. "mpv not found —
  playback disabled").
- Use `lipgloss.Width`/`Height` for layout math on styled strings, never `len()`.

Mock data lives in `internal/ui/mock.go` (provided in task prompt) until the
real library lands.

## cmd/tubeamp

`main.go`: parse flags (`-theme <name>` override, `-version`); `config.Load`;
`theme.Load` (fall back to `theme.Default()` with a warning); `player.New`
(on error: nil player, app shows degraded notice); `ytm.LoadAuth(DataDir()/auth)`
(missing file → nil auth) + `ytm.NewClient`; `core.NewQueue`; `ui.New`;
`tea.NewProgram(..., tea.WithAltScreen())`. On exit: `player.Close()`.
