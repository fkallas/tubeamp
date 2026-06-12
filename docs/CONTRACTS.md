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
  `github.com/charmbracelet/bubbles/textinput`,
  `github.com/charmbracelet/x/ansi` (ANSI-aware splicing in the overlay
  compositor; already in the module graph via lipgloss), and `gopkg.in/yaml.v3`.
  `internal/auth` shells out to the `yt-dlp` binary (already required for
  playback) to read browser cookies for `tubeamp -auth` — no extra Go dependency.
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

## internal/model

```go
type Track struct {
    VideoID  string
    Title    string
    Artists  []string
    Album    string
    AlbumID  string // album's MPRE… browseId; "" when unknown. Lets the UI open the album from a song row.
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
    Theme      string `yaml:"theme"`       // default "cyberpunk"
    Volume     int    `yaml:"volume"`      // 0-100, default 80
    MPVPath    string `yaml:"mpv_path"`    // default "mpv" (PATH lookup)
    YTDLFormat string `yaml:"ytdl_format"` // default "bestaudio"
    ArtPalette string `yaml:"art_palette"` // ArtPaletteAuto (default) or ArtPaletteTheme
    AuthUser   int    `yaml:"auth_user"`   // X-Goog-AuthUser account index, default 0 (multi-account)
    AuthBrowser string `yaml:"auth_browser"` // browser a sign-in was imported from (tubeamp -auth); ""=none.
                                              // Lets the UI re-import to refresh a stale session.
}

const (
    ArtPaletteAuto  = "auto"  // covers keep their own colors (median-cut)
    ArtPaletteTheme = "theme" // covers snap to the active theme's palette
)

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
`tokyo-night`, `cyberpunk` (neon: magenta/cyan on deep purple). User themes are
the same YAML shape.

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
(no API key needed). Authenticated requests add the `Cookie` header (from
`Auth.Header()`, the live merged set — NOT the frozen load-time value) plus
`Authorization: SAPISIDHASH <ts>_<sha1hex(ts + " " + SAPISID + " " + origin)>`
and `X-Origin`/`Origin: https://music.youtube.com`. The `X-Goog-AuthUser` header
carries the configured account index (`config.AuthUser`, default 0) set via
`SetAuthUser`, not a hardcoded "0".

Cookie rotation: Google attaches `Set-Cookie` (SIDCC, __Secure-1PSIDCC, sometimes
__Secure-*PSIDTS) to many responses. After every request `Client.post` folds
`resp.Cookies()` into the `Auth` cookie jar (new/changed values win; Max-Age=0 or
expired names dropped). When the set changes, `Auth` re-serializes to the
canonical `name=value; name=value` header — original order preserved, genuinely
new names appended — and rewrites the source auth file atomically (temp+rename,
0600). The rewrite first **three-way-merges the file's current contents**
(judged against the state at the Auth's last load/persist): a name only another
writer changed is adopted, a name only this Auth changed keeps its value, and a
per-name conflict defers to the on-disk value. That keeps several live Auths
bound to the same path — a `-status` probe beside a running TUI, or a superseded
client after a browser re-import — from clobbering each other's rotations
(plain whole-jar persistence would be last-writer-wins). The merged set is used
live for subsequent requests in the same process, so a healthy session stays
alive instead of decaying from the moment the cookie was copied. The jar + file
write are mutex-guarded (the daemon makes overlapping requests), and
`Client.post` reads the Cookie header and SAPISID as ONE locked snapshot so a
rotation cannot pair a new cookie with a hash of the old SAPISID. An `Auth`
with no source path updates memory only (never writes).

```go
// package ytm
// Auth holds the cookie set and keeps it alive across Google's rotation.
// The live (possibly rotated) set is maintained internally and mutex-guarded;
// must be passed by pointer (not copied).
type Auth struct {
    Cookie string // raw Cookie header AS ORIGINALLY LOADED (snapshot; use Header() for live)
    // unexported: ordered live jar, source path, generation counter, mutex
}
func LoadAuth(path string) (*Auth, error) // plain-text file, one line; DataDir()/auth by convention;
                                          // binds path so rotated cookies persist back there
func WriteAuthFile(path, cookieHeader string) error // SHARED atomic 0600 writer (same path Auth uses to
                                          // persist rotations); creates the parent dir 0700; trims +
                                          // appends a newline; rejects an empty header. Used by the
                                          // browser-import flow so the file write is not duplicated.
func (a *Auth) SAPISID() (string, error)  // from the LIVE set (SAPISID or __Secure-3PAPISID); a rotated value takes effect
func (a *Auth) Header() string            // current Cookie header to send (live merged set, canonical order)
func (a *Auth) Generation() uint64        // bumps on every absorbed rotation; UI polls it on its auth re-check (no goroutine/channel)

type Client struct{ ... }
func NewClient(a *Auth) *Client // a may be nil => unauthenticated (search still works)
func (c *Client) SetAuthUser(n int)     // X-Goog-AuthUser index for multi-account (clamped >=0)
func (c *Client) Authenticated() bool   // an auth cookie was loaded (NOT a live sign-in check)
func (c *Client) Search(ctx context.Context, query string) ([]model.Track, error)       // songs filter
func (c *Client) SearchWithAlbums(ctx context.Context, query string) (tracks []model.Track, derivedAlbums []model.Album, err error)
                                                  // one request, songs filter; derivedAlbums are the album refs
                                                  // carried by the song rows (full-catalog), bypassing the degraded
                                                  // album vertical. Year is empty until GetAlbum is opened.
func (c *Client) SearchAlbums(ctx context.Context, query string) ([]model.Album, error) // albums filter (dedicated vertical)
func (c *Client) GetAlbum(ctx context.Context, browseID string) (model.Album, []model.Track, error)
                                                  // browse endpoint {browseId: ...}; sets Album.BrowseID = browseID
                                                  // and every returned Track.AlbumID = browseID (album rows belong to it)

// Library / playlists (browse, same WEB_REMIX context). REQUIRE a live signed-in
// session: an anonymous (or stale-cookie) session makes YouTube return the
// logged-out page, which these surface as ErrNotSignedIn — a typed error, NOT an
// empty success, so the UI can tell "empty library" from "not signed in".
var ErrNotSignedIn = parse.ErrNotSignedIn // aliased so errors.Is works on ytm.<method> results

func (c *Client) LibraryPlaylists(ctx context.Context) ([]model.Playlist, error)
                                                  // browseId FEmusic_liked_playlists; skips the synthetic
                                                  // "New playlist" tile; Playlist.ID has any "VL" prefix stripped
func (c *Client) LikedSongs(ctx context.Context) ([]model.Track, error)
                                                  // Liked Songs auto-playlist (browseId "VLLM"); first page (~100) only
func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]model.Track, error)
                                                  // browseId "VL"+playlistID (accepts an already-prefixed id);
                                                  // first page (~100) only
func (c *Client) AccountInfo(ctx context.Context) (name string, signedIn bool, err error)
                                                  // account/account_menu; signedIn = activeAccountHeaderRenderer
                                                  // present, name from its accountName runs. Logged-out menu =>
                                                  // ("", false, nil) (a result, not an error). HTTP errors => err.
func (c *Client) Lyrics(ctx context.Context, videoID string) (string, error)
                                                  // plain-lyrics fallback (used when LRCLIB has none). Two-call
                                                  // InnerTube flow: POST next {videoId} -> lyrics-tab browseId
                                                  // (parse.LyricsBrowseID) -> POST browse {browseId} ->
                                                  // description text (parse.LyricsText). ErrNoLyrics when absent.

var ErrNoLyrics = parse.ErrNoLyrics // aliased so errors.Is works on ytm.Lyrics results

// package ytm/parse — ALL response JSON parsing lives here, fixture-tested.

// SearchResult bundles a song-search response: the tracks plus the album refs
// derived from those same song rows (each full-catalog song carries its album's
// MPRE… browseId). Derived albums work around YouTube's degraded album-search
// vertical for anonymous sessions.
type SearchResult struct { Tracks []model.Track; Albums []model.Album }

func SearchResults(raw []byte) (SearchResult, error) // songs shelf: tracks + album refs derived from the
                                                  // song rows (Album: BrowseID + Title from the MPRE… album
                                                  // run, Artists = song artists, ThumbURL = song thumb,
                                                  // Year empty), deduped by BrowseID in first-seen order
func SearchTracks(raw []byte) ([]model.Track, error)                  // thin wrapper over SearchResults (tracks only)
func SearchAlbums(raw []byte) ([]model.Album, error)                  // albums search shelf + top-result card
func AlbumPage(raw []byte) (model.Album, []model.Track, error)        // album header + track shelf
func AccountInfo(raw []byte) (name string, signedIn bool, error)      // account/account_menu menu (both shapes)

// Lyrics (plain fallback). LyricsBrowseID walks a `next` watch response for the
// lyrics-tab browseId (an "MPLYt…" id; matched by tab title "Lyrics" OR the
// MPLYt prefix). LyricsText pulls the musicDescriptionShelfRenderer.description
// (runs or simpleText). Both return ErrNoLyrics (typed) when absent; only
// malformed JSON errors. Defensive, fixture-tested (lyrics_next.json + lyrics_browse.json).
func LyricsBrowseID(raw []byte) (string, error)
func LyricsText(raw []byte) (string, error)
var ErrNoLyrics = errors.New("ytm: no lyrics") // no lyrics tab / no description shelf

var ErrNotSignedIn = errors.New("ytm: not signed in") // logged-out page. Detected positively via the
                                                  // responseContext "logged_in" marker ("0" => logged out;
                                                  // "1" + expected renderer absent => signed-in EMPTY page,
                                                  // an empty success); renderer absence alone decides only
                                                  // when the marker is missing
func LibraryPlaylists(raw []byte) ([]model.Playlist, error) // FEmusic_liked_playlists gridRenderer;
                                                  // skips the "New playlist" tile and non-playlist tiles
                                                  // (pageType / VL-PL id prefix checked); logged_in "0" =>
                                                  // ErrNotSignedIn even if a stray grid is present
func PlaylistTracks(raw []byte) ([]model.Track, error)     // playlist/Liked-Songs musicPlaylistShelfRenderer;
                                                  // a present shelf always parses (public playlists browse
                                                  // fine anonymously); present-but-empty shelf => empty
                                                  // slice; shelf absent => empty slice when logged_in "1",
                                                  // else ErrNotSignedIn
// All defensive: skip malformed items, never panic. AlbumPage handles BOTH
// header shapes (musicDetailHeaderRenderer and musicResponsiveHeaderRenderer);
// per-track artists fall back to album artists, Track.Album = album title,
// Track.ThumbURL = album thumb. The returned Album has no BrowseID (caller sets it),
// so AlbumPage's tracks carry AlbumID = "" — ytm.GetAlbum stamps the real browseId
// onto each. Every song-row parser (SearchResults/SearchTracks, PlaylistTracks,
// LikedSongs) threads the row's album MPRE… browseId through to Track.AlbumID
// ("" when the row links to no album).
```

Response shapes change under us; parsers must tolerate missing keys. Mark the
songs-/albums-filter `params` constants with a `// TODO: verify against ytmusicapi`
comment. Parser fixtures in `parse/testdata/` (`search_songs.json`,
`search_albums.json`, `album_page.json`) are trimmed live captures;
`search_songs_albumrefs.json` is handcrafted to exercise `SearchResults`'
derived-album dedup (two song rows share one MPRE… album, one row has no album);
`album_page_detail.json` is
handcrafted to exercise the older `musicDetailHeaderRenderer` shape.
`account_signed_in.json` is handcrafted to exercise the signed-in `AccountInfo`
shape (with `activeAccountHeaderRenderer`); `account_logged_out.json`,
`library_playlists_logged_out.json` and `playlist_tracks_logged_out.json` are
trimmed live anonymous captures of the logged-out shapes, cross-checked by the
credential-free anonymous live tests (gated on `TUBEAMP_LIVE=1`).
`library_playlists.json` / `playlist_tracks.json` are handcrafted (grid +
playlist-shelf), each with one malformed item skipped. `lyrics_next.json` /
`lyrics_browse.json` are a handcrafted next+browse pair exercising the two-call
lyrics flow (a "Lyrics" tab with an MPLYt browseId, then a description shelf).

## internal/lyrics

Fetching and parsing of song lyrics — synced (LRC, from LRCLIB) and plain. Pure
parsing (`ParseLRC`/`CurrentLine`) is table-tested; `FetchLRCLIB` is tested
against an `httptest` server (the HTTP client + base URL are injectable package
vars, never the real network). LRCLIB is plain HTTP+JSON over `net/http` — no
new dependency; the LRC body is parsed here. A YouTube Music plain-text fallback
lives in `ytm.Client.Lyrics`.

```go
const (
    SourceLRCLIB  = "lrclib"  // synced/plain lyrics from lrclib.net
    SourceYTMusic = "ytmusic" // plain lyrics from YouTube Music's InnerTube API
)

type Line struct { At time.Duration; Text string } // At = start offset; Text "" for a blank cue

// Lyrics is a resolved result. Synced=true when At timestamps are present (LRC);
// Lines then holds the timed lines and Plain may still hold the unsynced text.
// For plain results Synced=false, Lines empty, Plain holds the text. Source is
// SourceLRCLIB/SourceYTMusic or "".
type Lyrics struct { Lines []Line; Synced bool; Plain string; Source string }

// ParseLRC parses an LRC body into timed lines, sorted by ascending At.
// Understands "[mm:ss.xx]" and "[mm:ss]" (1-3 fractional digits); a single line
// may carry several leading timestamps (one Line emitted per timestamp).
// Metadata tags ([ar:]/[ti:]/[al:]/[length:]/[by:]/…) and timestamp-less lines
// are ignored; malformed timestamps skipped. [offset:NNN] (ms) is honoured —
// per LRC convention a positive offset shifts lines EARLIER (At reduced),
// negative later; negative results clamp to 0. Pure.
func ParseLRC(s string) []Line

// CurrentLine returns the index of the last line with At <= pos, or -1 before
// the first line / for empty input. Binary search; assumes lines sorted by At.
func CurrentLine(lines []Line, pos time.Duration) int

var ErrNoLyrics = errors.New("lyrics: no lyrics found") // typed "nothing found"; match with errors.Is

// FetchLRCLIB looks up lyrics on LRCLIB. GET /api/get?artist_name=&track_name=
// &album_name=&duration=<secs> with UA "tubeamp (https://github.com/fkallas/tubeamp)".
// 200 => prefer syncedLyrics (ParseLRC, Synced=true) else plainLyrics. 404 =>
// GET /api/search?q=<artist title>, first hit with synced (else first hit's
// plain). Nothing usable => ErrNoLyrics; network/HTTP/decode => wrapped error.
// io.LimitReader (~1MB); ctx bounds both requests.
func FetchLRCLIB(ctx context.Context, artist, title, album string, dur time.Duration) (Lyrics, error)

// httpClient (a Doer) and baseURL are unexported package vars, overridable in tests.
```

## internal/auth

One-command sign-in by importing the YouTube/Google cookies from a local browser
**via yt-dlp's `--cookies-from-browser`** (yt-dlp is already a hard dependency for
playback), so users do not hand-copy a Cookie header out of devtools.
`AssembleCookieHeader` and `parseNetscapeCookies` (pure, fixture-tested) are split
from `ImportFromBrowser` (the yt-dlp shell-out) so the parsing/assembly is
unit-tested without a real browser.

Why yt-dlp rather than a native cookie-store reader: yt-dlp reads the LIVE cookies
(it applies the SQLite WAL), so a running browser is handled correctly — a naive
on-disk-snapshot reader returns a stale set that resolves anonymous for a
genuinely signed-in user. yt-dlp also handles profile selection and the full
Chrome-family decryption (macOS Keychain, app-bound encryption). tubeamp's
InnerTube request recipe is correct as-is; the ONLY auth failure mode that
remains is cookie ROTATION (Google rotates `__Secure-*PSIDTS` within minutes), not
request construction — verified by ytmusicapi resolving the same fresh/stale
cookies identically.

```go
// AssembleCookieHeader builds the canonical "name=value; …" Cookie header from a
// set of browser cookies: dedupe by name (first non-empty wins), drop empties,
// emit known sign-in cookies in a fixed canonical order then extras alphabetically.
// Returns ErrNoSAPISID when no SAPISID/__Secure-3PAPISID is present (logged-out
// set) — this is the typed "no usable cookie set" signal.
func AssembleCookieHeader(cookies []*http.Cookie) (string, error)

// ImportFromBrowser shells out to `yt-dlp --cookies-from-browser <browser>` into a
// temp Netscape jar (the jar path must NOT pre-exist — yt-dlp treats --cookies as
// an input too and rejects an empty file; an MkdirTemp dir + non-existent file is
// used), parses the .youtube.com/.google.com cookies, and assembles the header.
// browser ∈ {chrome,chromium,edge,brave,firefox,safari,opera,vivaldi}; "" or
// "auto" tries autoOrder and returns the first browser yielding a SAPISID-bearing
// set. sourceBrowser names the browser that supplied the header (concrete
// lowercase id, never "auto"; "" on error) — the -auth command uses it for the
// advice text and the persisted cfg.AuthBrowser. ErrYTDLPMissing when yt-dlp is
// absent; ErrNoStore when a browser yields nothing; ErrNoSAPISID when cookies were
// read but none carried a session.
func ImportFromBrowser(browser string) (cookieHeader, sourceBrowser string, err error)

// IsBrowserRunning reports whether the named browser process is running (pgrep on
// darwin/linux). Best-effort: unsupported browser / "" / "auto" / non-darwin-linux
// / missing pgrep all report false. Retained as a utility; the -auth advice no
// longer depends on it (yt-dlp reads running browsers fine).
func IsBrowserRunning(browser string) bool

var ErrNoSAPISID    = errors.New(...) // no signing cookie in the set
var ErrNoStore      = errors.New(...) // browser yielded no usable cookies
var ErrYTDLPMissing = errors.New(...) // yt-dlp not on PATH
```

A real-browser read is gated behind `TUBEAMP_LIVE_IMPORT=1` (`t.Skip` by default,
optional `TUBEAMP_LIVE_IMPORT_BROWSER`); it never logs the cookie value. The real
process check is gated behind `TUBEAMP_LIVE_PROC=1` (optional
`TUBEAMP_LIVE_PROC_BROWSER`).

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
tubeamp                              v0.1.0   ● account   ← wordmark header (1 row)
╭─1 Library──╮╭─4 <context title> ─────────────╮
│            ││                                 │
╰────────────╯│      main view                  │
╭─2 Playlists╮│                                 │
│            ││                                 │
╰────────────╯╰─────────────────────────────────╮
╭─3 Queue────╮│ Lyrics                 ♪ synced  │  ← only while playing + tall
│            ││   …synced lyrics window…         │     enough; focusable when shown
╰────────────╯╰─────────────────────────────────╯
╭─ player bar (art 8×4 cells │ title/artist/album │ progress, vol) ─╮
╰───────────────────────────────────────────────────────────────────╯
 <context-sensitive key hints, single line>
```

Wordmark header: 1 row (`logoHeight`), a plain "tubeamp" wordmark (7 letters) in
the top-left whose letters cycle through the theme's colours as an animation —
letter `i` takes `theme.Palette()[(i+phase) % len(palette)]` (image/color values
converted to lipgloss colours), so advancing `phase` flows the colours across the
word. A `tea.Tick` (~250ms, started in `Init` and re-issued on each
`logoTickMsg`) increments the phase (a model field) and the view re-renders; the
tick is cheap and independent of every other Cmd. The Muted "v0.1.0" version tag
and the sign-in indicator ride the same row, right-aligned. When terminal height
< `logoMinTermHeight` (= `minHeight + logoHeight`) the header is hidden and the
panel area reclaims that row (the panel-area floor needs that row at the shortest
size). Colours come strictly from theme tokens/palette — no hardcoded hex.

Sign-in indicator: when the ytm client is non-nil the model fires a one-shot
`AccountInfo` Cmd on startup (`Init`); the resolved state is shown persistently
at the right end of the wordmark header row (ANSI-aware-truncated with an
ellipsis when a long account name would not fit), or right-aligned on the bottom
status line when the header is hidden (truncated the same way — on either home an
oversized indicator must clip, never widen the row past the terminal and break
the full-width frame invariant). Signed in => "● <name>" in PlayingStyle; an auth
file that resolves anonymous (a stale cookie) => "○ anonymous — cookie stale?
see README" in Muted; no auth file => "○ not signed in" in Muted. A failed check
(network down) leaves the indicator blank (no retry). A library browse that
later returns `ErrNotSignedIn` downgrades the resolved state to anonymous (the
cookie rotated mid-session), flipping the indicator to the stale-cookie hint.
Tests inject the result via `accountInfoMsg`, never the network.

**Stale-session auto-refresh.** When the resolved state is anonymous (the startup
`AccountInfo`, or a library browse via the downgrade path) AND `cfg.AuthBrowser`
is set AND an auth file is present (`hasAuth`), the model fires a ONE-SHOT
re-import `Cmd` — guarded by `reimportTried` so it happens at most once per
session (no reimport loop). The Cmd runs the injectable `reimportFn`
(default `defaultReimport`: `auth.ImportFromBrowser(cfg.AuthBrowser)` →
`ytm.WriteAuthFile` → `LoadAuth` → rebuild `Client` → `AccountInfo`), returning a
`reimportMsg`. On a signed-in result the model adopts the fresh client, flips the
indicator to signed-in, toasts `session refreshed from <browser>`, and fills the
Playlists panel. On a failed import (no fresh client) or a CONFIRMED anonymous
result it toasts `re-import failed — run tubeamp -auth <browser>`. When the
import succeeded but only the confirmation probe failed (offline/timeout), the
fresh client is still adopted — the new cookies are on disk and must serve (and
persist rotations for) subsequent requests — the resolved indicator state is
left untouched (unconfirmed, mirroring the startup `accountInfoMsg` handler),
and it toasts `re-imported from <browser> — could not confirm sign-in`. The
re-import never blocks `Update` (it is a `tea.Cmd`). Tests inject `reimportFn`
as a stub (signed-in stub ⇒ indicator flips + toast; failing stub ⇒ fallback
toast; probe-error stub ⇒ client adopted, no failure toast; fires at most once).

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
  table, then a Muted "Albums" header + album rows `▤ <Title> — <Artists> (<Year>)`
  (the `(<Year>)` segment is omitted when empty, e.g. for derived albums). A single
  selection cursor runs through both sections (songs first, then albums) so `j`/`k`
  move through them seamlessly. The Albums section is **merged** from two sources:
  albums *derived from the song hits* (`SearchWithAlbums`, full catalog) come FIRST,
  then the dedicated albums-vertical results (`SearchAlbums`, degraded for anonymous
  sessions), deduped by BrowseID — an album present in both sources keeps its
  derived-first position and title but takes the vertical's richer
  Artists/Year/ThumbURL. The two responses arrive separately and the merge is
  recomputed as each lands; when the songs land after the albums vertical, a
  highlighted album row keeps its selection (the cursor follows the album to its
  new combined index). Both `enter` (play album) and `o` (album view) drive
  `GetAlbum` off the row's BrowseID, so a derived album's missing metadata fills
  in when it is opened.
- **album view** (`panels.AlbumView`) — a header row with a large
  `panels.AlbumCoverCols`×`panels.AlbumCoverRows` (16×8) pixel-art cover on the
  left and the album title (Accent), artists, year + track count (Muted) to its
  right; below, the album's track list (number, title, duration — no album column).

### Lyrics panel (`panels.LyricsView`)

A lyrics panel rides in the RIGHT column, below the main view and above the
player bar. It is a `focusArea` (`focusLyrics`) but **focusable ONLY WHILE
VISIBLE**: it has NO number key (`1`/`2`/`3`/`4` stay the four main panels) and
is reached only via the `h`/`l` cycle, which now runs
Library→Playlists→Queue→Main→Lyrics→wrap (`focusCount` = 5), **skipping Lyrics
whenever it is hidden** (`m.lyricsVisible()`). If focus is on Lyrics and the
panel hides (playback stops / terminal too short), focus falls back to Main
(`reconcileLyricsFocus`, called from the resize, player-event and clear-queue
paths).

- **Visibility.** Shown only when a track is playing (`m.hasNow`) AND the top
  area is tall enough to fit it without starving the main view: the band is a
  fixed share clamped so the main view always keeps `>= lyricsMainMinH` (8) box
  rows; below `lyricsBandMin` (6) usable rows the panel is hidden and the main
  view reclaims them (layout then identical to before). When shown the band is at
  most `lyricsBandRows` (10) tall. The split is of the right column only, so the
  rendered view still totals exactly the terminal height with full-width rows at
  every size (measured with `lipgloss.Height`/`Width`).
- **Content.** A bordered box (active border + active title when focused, the
  theme inactive border otherwise — like any panel) titled "Lyrics" with a
  right-aligned state marker (`♪ synced` in Accent / `unsynced` Muted /
  `no lyrics` Muted / `⏸ paused — esc to follow` Muted while a focused synced
  peek is detached; no marker while loading). Synced (following): a window of
  lines centered on the current line — current in PlayingStyle, immediate
  neighbours in Primary, the rest Muted — auto-scrolling as playback advances.
  Unsynced: the plain text from the scroll offset in Primary. Loading:
  "searching for lyrics…" (Muted). None: "No lyrics found." (Muted). Colours come
  strictly from theme tokens.
- **Sync source.** When unfocused (or focused+following) the highlighted line is
  driven off the SAME playback position the progress bar uses (`m.timePos`,
  updated by `EvTimePos`), via `lyrics.CurrentLine` — no new event wiring.
- **Scroll (focused only).** `j`/`k`, `ctrl+u`/`ctrl+d` (reusing `halfPageRows`),
  `g`/`G` scroll the panel. **Unsynced:** a plain top-line scroll offset, clamped
  to content. **Synced:** PEEK-scroll — it temporarily DETACHES auto-follow
  (`lyricsDetached`), showing the user's scrolled line index (`lyricsScroll`)
  instead of the live one. A detached peek re-engages auto-follow (snapping back
  to the live line) when (a) `esc` is pressed while `focusLyrics` is active and
  detached — the esc is CONSUMED and does NOT pop the main-view stack (esc only
  pops the stack when lyrics is not focused, or focused but not detached); or
  (b) after ~5s of playback with no further scroll — an idle timer with NO new
  ticker: each scroll arms `lyricsResumeAt = m.timePos + lyricsFollowResumeSec`
  (5s) and a later `EvTimePos` past it re-engages (`maybeResumeLyrics`). Leaving
  `focusLyrics` (h/l away, panel hides, or a track change) also re-engages follow.
  Detached state ONLY applies while `focusLyrics` is active. Test seams:
  `Model.LyricsScrollOffset()` and `Model.LyricsDetached()`.
- **Fetch + cache.** On a track change (new videoID) the lyrics state is
  set to "loading" and a `tea.Cmd` (never blocking `Update`) calls
  `lyrics.FetchLRCLIB` (8s ctx); on `lyrics.ErrNoLyrics` it falls back to
  `ytm.Client.Lyrics` (plain text) when the client is non-nil. Resolved results
  are cached by videoID (`lyricsCache`) so replays/seeks never refetch; the
  loading entry itself dedupes in-flight lookups (at most one fetch per videoID
  ever runs). There is deliberately NO generation guard (unlike
  `searchGen`/`albumGen`): the cache is keyed by videoID and only the CURRENT
  track's entry is displayed, so a late result — even for a track already moved
  past — is always valid for its own key and is recorded unconditionally
  (dropping it would leave that key stuck on its loading entry, sticking the
  panel on "searching" when the user returns to the track). nil player / nil
  client paths never panic. The lookup itself is behind the injectable
  `lyricsFetch` package var so tests never hit the network.

### Keymap (package keymap, bubbles/key bindings; this is the spec reviewers check)

Global: `1` focus Library; `2` focus Playlists; `3` focus Queue; `4` focus Main
view; `h`/`l` cycle panel focus backward/forward
(Library→Playlists→Queue→Main→Lyrics→wrap, skipping Lyrics while hidden);
`←`/`→` seek -5s/+5s; `space` pause; `n`/`p` next/previous track;
`↑`/`↓` (also `+`/`=`/`-`) volume; `m` mute; `/` search overlay; `T` theme picker; `?` help
overlay; `q`/`ctrl+c` quit; `esc` closes overlay / re-engages a detached lyrics
peek / pops view stack.

Per panel: `j`/`k` move selection; `ctrl+d`/`ctrl+u` half-page down/up (clamped
at list edges); `g/G` top/bottom; `enter` activates. A search-results frame
whose cursor was never deliberately moved resets to the top when the
songs/albums sections finish arriving. When the **Lyrics** panel is focused these
same bindings scroll the lyrics instead (unsynced = plain scroll; synced =
peek-scroll that detaches auto-follow — see the Lyrics panel section).
Library/Playlists `enter` → load tracks into the main view (see "Library &
playlists" below for the real-vs-mock split). Main view
`enter` → `queue.Set(visibleTracks, cursor)` + play; `a` append to queue;
`A` insert-next; `o` open album — on a search **album row** the album by its own
browseId, on a **song row** (plain track list, the search Songs section) the
song's album via `Track.AlbumID` (toast "no album for this track" when empty).
Queue: `enter` jump-to-track, `d` remove, `J/K` move item, `c` clear, `o` open the
selected track's album.

Album flows (main view): a search produces two sections — Songs then Albums (see
below). On an **album row**: `enter` fetches `GetAlbum` then `PlaylistReplace`s the
queue with the album from track 0 and plays ("Playing <album>" toast); `o` fetches
`GetAlbum` then pushes a dedicated **album view** onto the main-view stack. On a
**song row** (any track list, the search Songs section, or a Queue panel row),
`o` opens that song's album the same way — `GetAlbum(Track.AlbumID)` through the
same generation guard and album-view push as the album-row `o`; a song with no
`AlbumID` toasts "no album for this track" and pushes nothing. The album view
itself does not bind `o` (its rows already belong to the album on screen). In the
**album view**: `j`/`k` move; `enter` = `PlaylistReplace(albumTracks, selected)` +
play (the whole album, starting at the selected track); `esc` pops back to the
search results with the cursor preserved. GetAlbum carries a generation guard
(like the search guard — invalidated by a newer fetch, by `esc`, and by replacing
the main view) so a stale album page is dropped; album covers need no guard —
they are content-addressed by browseID and cached on arrival, so a late download
is never wrong. nil-player / nil-client are handled with status-line notices,
never a crash.

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
  Cmd (`art.Fetch` + `art.Render` with options from `Model.artOptions()`:
  `cfg.ArtPalette` "auto" → median-cut of the cover's own colors, "theme" →
  snap to `theme.Palette()`; cache keyed by `Model.artKey` — theme-dependent
  only in theme mode); fallback/loading state `art.Placeholder`.
  Mock thumbs: `https://i.ytimg.com/vi/<videoID>/mqdefault.jpg`.
- Theme picker overlay: lists `theme.List(config.ThemesDir())`, live-previews
  on cursor move, `enter` = keep + `cfg.Save()` (via Cmd), `esc` = revert.
- Search overlay: `bubbles/textinput`; on enter, if ytm client non-nil run
  `SearchWithAlbums` (songs + albums derived from the song rows) and `SearchAlbums`
  (the dedicated albums vertical) Cmds concurrently (`tea.Batch`, both 5s timeout
  ctx) → results become a search-results frame (Songs + Albums sections); render
  whichever returns first, fill the other section on arrival. The Albums section
  merges derived-from-songs albums first, then the vertical results, deduped by
  BrowseID. On error or nil client, status-line message.
- Album cover (album view): fetch via Cmd from `RewriteThumbURL(thumb, 64)`,
  render 16×8 with the same `artOptions()`/`artKey` scheme as the player bar,
  `Placeholder` while loading.
- Status line doubles as transient error/info toast (e.g. "mpv not found —
  playback disabled").
- Use `lipgloss.Width`/`Height` for layout math on styled strings, never `len()`.

### Library & playlists (real vs mock)

For a **signed-in** session (client non-nil and the one-shot `AccountInfo` check
resolved `signedIn`), real data replaces the mock:

- On that sign-in result the model fires `LibraryPlaylists` (guarded by `plGen`)
  and replaces the Playlists panel with the user's real playlists.
- Library "Liked Songs" `enter` → `LikedSongs` Cmd into the main view; a playlist
  `enter` → `PlaylistTracks` into the main view titled by the playlist name —
  but only once the panel actually holds real playlists: while the
  `LibraryPlaylists` fill is still in flight (or failed) the rows are mock data
  and play their mock tracks locally; a mock ID never reaches a real browse.
  Both real loads show a "loading …" status while in flight and are guarded by
  `libGen` (a stale result — the user navigated away, a newer load, esc,
  issuing a new search, replacing the main view, or opening an album view
  ('o' is reachable mid-load, e.g. from a queue row) — is dropped, mirroring
  the search/album generation pattern).

For an **anonymous** session the mock data is kept. With a client present (the
check resolved not-signed-in), "Liked Songs"/playlist `enter` loads the mock
tracks and toasts "sign in to load your library — see README"; with no client at
all the mock loads silently (no hint — there is nothing to sign in to). The
browse methods returning `ytm.ErrNotSignedIn` (matched with `errors.Is`) keep
the mock data, toast the same hint, and downgrade the resolved sign-in state to
anonymous (the cookie rotated mid-session), so the indicator and the library
behavior stay consistent.

The other Library items (Albums/Artists/Songs/History) remain mock for now.
Tests inject `libPlaylistsMsg` / `libTracksMsg` (and `accountInfoMsg`); the real
browses never run from tests. Mock data lives in `internal/ui/mock.go`.

## cmd/tubeamp

`auth.go`: `-auth <browser>` runs the one-command browser import and exits (it
does not open the TUI or touch the daemon). It imports via `auth.ImportFromBrowser`
(stubbable `authImport` package var) and ATTRIBUTES the import to the
`sourceBrowser` it reports — so a bare `-auth` (auto) resolves to the concrete
browser whose store won, and every downstream use (the running check, the advice
text, the persisted `cfg.AuthBrowser`) names that browser, never the literal
"auto". It writes the auth file with the shared
`ytm.WriteAuthFile`, persists the resolved browser as `cfg.AuthBrowser`, then confirms with
a bounded `AccountInfo` (stubbable `confirmSignIn` var, which returns the probe
error distinctly) — printing `signed in as <name>`, or the anonymous advice from
the pure `anonymousAdvice(browser, running, signedIn)` selector, or — when only
the confirmation probe failed (offline/timeout; the import itself succeeded) —
`imported from <browser>, but could not confirm the sign-in (<err>) — check
later with tubeamp -status`. The anonymous advice depends on whether the browser
is running (`auth.IsBrowserRunning`, via the stubbable `browserRunning` var): a
RUNNING browser yields `<browser> is running — cookies copied from a running
browser are often stale. Quit <browser> completely (Cmd-Q) and run 'tubeamp -auth
<browser>' again.` (the running browser holds the fresh login in memory/WAL,
which the on-disk snapshot lacks); otherwise the `are you logged into <browser>?
Try signing in there, or import from another browser.` hint. The detection never
fails the command. NEVER prints cookie values. The `-auth` flag accepts a
value (`-auth chrome`, `-auth=chrome`) and stands alone (bare `-auth` ⇒ "auto"
via a custom `flag.Value` with `IsBoolFlag`; the space form `-auth chrome` is
recovered from the trailing positional).

`main.go`: parse flags; `config.Load`. With no control flag set it runs the TUI:
`-theme <name>` override, `-version`; `theme.Load` (fall back to `theme.Default()`
with a warning); `player.New` (attach-or-spawn; on error nil player + degraded
notice); `ytm.LoadAuth(DataDir()/auth)` (missing → nil auth) + `ytm.NewClient`
then `client.SetAuthUser(cfg.AuthUser)`; `core.NewQueue`; `ui.New`;
`tea.NewProgram(..., tea.WithAltScreen())`. On exit: `player.Close()` — a DETACH,
so mpv keeps playing in the background.

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
| `-status`   | multi-line human-readable status (glyph, title, artists, album, pos/dur, volume, track n/m) + a `yt music: signed in as <name> / anonymous / unknown` line (best-effort, 2s-bounded — never fails or stalls `-status`) |
| `-line`     | one compact line `♪ Title — Artist 1:23/3:54` (~48 cols); empty when idle or no daemon |
| `-queue`    | numbered queue, playing row marked `▶`                               |
| `-kill`     | `player.Quit()` the daemon (removes socket + lock)                    |
