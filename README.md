# tubeamp

A Lazygit-inspired terminal UI for YouTube Music, written in Go.

Stacked, numbered panels on the left (Library / Playlists / Queue), a contextual
main view on the right, a persistent player bar with pixel-art album covers, and
number-key panel switching with arrow-key navigation. Themeable via simple YAML files.

The main view lists tracks as a table — title, artist, album, and duration —
with the album column hidden automatically on narrow terminals.

**Playback survives the TUI.** mpv runs as a persistent, detached background
daemon that owns the playlist; the TUI is just a client that attaches to it.
Quit the TUI and the music keeps playing — reopen it and it reattaches to the
in-progress track. The same daemon is controllable from the command line so you
can wire keybindings and status bars to it.

## Status

Early scaffold. The TUI shell, theming, persistent/detached mpv playback (with a
playlist-backed queue, CLI control, and gapless prefetch), and the pixel-art
renderer work end to end. The YT Music API client wires unauthenticated
song/album search and album browsing into the search view. For a **signed-in**
session it also loads your real library: the Playlists panel fills with your
playlists on startup, Library → "Liked Songs" loads your Liked Songs, and opening
a playlist loads its tracks (first page, ~100 tracks each; radio not yet). An
**anonymous** session keeps the demo/mock library and toasts a sign-in hint. The
remaining Library sections (Albums/Artists/Songs/History) are still mock.

Time-synced lyrics are plumbed in as a building block (`internal/lyrics`): LRC
fetching/parsing from [LRCLIB](https://lrclib.net) with a YouTube Music
plain-text fallback (`ytm.Client.Lyrics`). Not yet surfaced in the UI.

Note: the InnerTube **album-search vertical** ranks oddly — it surfaces obscure
self-distributed releases over the major-label catalog. This is **not** an auth
problem (an authenticated session returns the same ranking; ytmusicapi behaves
identically). tubeamp works around it independently of sign-in: every *song*
result carries its album's browseId, so the Albums section is reconstructed from
the (full-catalog) song hits and shown first, with the album-vertical results
merged in after and deduped. Major-label albums therefore show up in search.
Signing in (see [Signing in](#signing-in)) unlocks your *library* (Liked Songs,
playlists) and is shown live in the header. Heads up: a cookie copied/imported
from a logged-in browser resolves as **anonymous** within minutes because Google
rotates session cookies continuously (see the cookie-rotation note below) — this
is the one real gotcha, and the bulk of "why am I anonymous?" confusion.

## Requirements

- Go 1.22+ (build)
- [mpv](https://mpv.io) — playback engine
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — stream resolution (used by mpv)

```sh
brew install go mpv yt-dlp
```

## Quickstart

```sh
go build ./cmd/tubeamp
./tubeamp
```

Keys: `1/2/3/4` jump to a panel, `h/l` cycle panels, `j/k` move (`ctrl+d`/`ctrl+u`
half-page), `enter` plays,
`space` pause, `n`/`p` next/previous, `←/→` seek ±5s, `↑/↓` or `+/-` volume, `/` search,
`o` open album — an album row, or the album the highlighted **song** belongs to
(works on song rows in search, your library, playlists, and the queue), `T` theme
picker, `?` help, `q` quit (playback keeps running in the background).

Search returns two sections — Songs and Albums. On an album row, `enter` plays the
whole album and `o` opens an album view (cover, metadata, track list) where `enter`
plays the album from the highlighted track and `esc` returns to the results. `o` on
a song row opens that song's album view too.

## Gapless playback

The daemon runs mpv with `--prefetch-playlist=yes --gapless-audio=weak`, so mpv
resolves and opens the **next** queue entry (through yt-dlp) slightly before the
current track ends and transitions without a gap when the codecs line up. Because
each entry is resolved on demand, the prefetch happens a moment before the
hand-off rather than far ahead; expect tight, near-gapless transitions rather than
sample-accurate ones for streamed sources.

## Controlling playback

`tubeamp` with no arguments opens the TUI. With a control flag it instead talks
to the running background daemon and exits — handy for global keybindings and
tmux/status-bar integrations. If no daemon is running these print
`tubeamp: not running` and exit 1 (except `-line`, which stays silent so an empty
status cell is shown).

| Command            | Effect                                                          |
|--------------------|-----------------------------------------------------------------|
| `tubeamp -p`       | toggle pause                                                    |
| `tubeamp -next`    | skip to the next track                                          |
| `tubeamp -prev`    | skip to the previous track                                      |
| `tubeamp -stop`    | stop playback                                                   |
| `tubeamp -vol N`   | set volume — `N` absolute, `+N`/`-N` relative (e.g. `-vol +5`)  |
| `tubeamp -seek S`  | seek `±S` seconds (e.g. `-seek -10`)                            |
| `tubeamp -status`  | human-readable status (title, artists, album, position, volume) + YT Music sign-in line|
| `tubeamp -line`    | one compact line `♪ Title — Artist 1:23/3:54`; empty when idle  |
| `tubeamp -queue`   | the numbered queue, playing row marked `▶`                      |
| `tubeamp -kill`    | quit the background daemon entirely                             |

## tmux status bar

Show the now-playing track on the right-hand side of your tmux status bar by
shelling out to `tubeamp -line`. Add this block to `~/.tmux.conf` (use the
absolute path to the installed binary, e.g. `$(go env GOPATH)/bin/tubeamp` after
`go install ./cmd/tubeamp`):

```tmux
set -g status-interval 5
set -ga status-right " #(/path/to/tubeamp -line)"
```

`-ga` *appends* to `status-right`, so it sits alongside whatever you already
display there. `status-interval 5` refreshes the cell every five seconds.
Because `-line` prints nothing when playback is idle (or no daemon is running)
and exits 0, the status cell stays clean instead of showing an error — the
now-playing line simply appears once you start a track.

## Signing in

tubeamp talks to YouTube Music's private InnerTube API. Search works logged out,
but signing in unlocks results tied to your account. Auth is a single cookie
header stored in a plain-text file:

```
~/.local/share/tubeamp/auth          # or $XDG_DATA_HOME/tubeamp/auth
```

**Recommended: import from your browser (one command)**

If you are already logged into YouTube Music in a browser, sign in with a single
command — tubeamp reads the sign-in cookies out of the browser (via yt-dlp's
`--cookies-from-browser`, the same yt-dlp you already have for playback) and
writes the auth file for you:

```sh
tubeamp -auth chrome     # or: chromium, edge, brave, firefox, safari, opera, vivaldi
tubeamp -auth            # bare -auth = "auto": try every supported browser
```

Reading through yt-dlp means a **running browser is fine** — it reads the live
cookies (the SQLite WAL), handles multiple profiles, and decrypts the
Chrome-family stores (on macOS the first Chrome read pops a Keychain consent
dialog — allow it). It then confirms with the live API and prints
`signed in as <name>`. A bare `tubeamp -auth` (auto) is attributed to the browser
that actually supplied the session, so messages name that concrete browser, never
"auto", and the source is remembered in `config.yaml` (`auth_browser`) for the
auto-refresh below.

> **If it comes back anonymous**, the cookies imported but YouTube resolved them
> logged out. Almost always this is **cookie rotation** (see below), not a tubeamp
> problem: confirm you're signed into YouTube Music in that browser and just run
> `tubeamp -auth <browser>` again — a fresh read usually lands signed-in. For a
> session that *stays* fresh, use the incognito trick described under
> "cookie-rotation" below.

yt-dlp must be on your PATH (it is, if playback works); `-auth` errors clearly if
it is missing.

**Fallback: copy the Cookie header by hand**

1. Open <https://music.youtube.com> in your browser and make sure you are logged in.
2. Open the developer tools (F12) → **Network** tab.
3. Click around (or reload) so a request to `music.youtube.com` appears, then
   select any such request.
4. Under **Request Headers**, find `Cookie:` and copy its **entire** value.
5. Paste it as a single line into `~/.local/share/tubeamp/auth` (create the
   directory if needed). No quotes, no `Cookie:` prefix — just the value.

The header in the logo area then shows your live sign-in state:

- `● <name>`  — signed in (your account name)
- `○ anonymous — cookie stale? see README`  — an auth file is present but
  YouTube resolved the request as logged out (almost always a stale cookie)
- `○ not signed in`  — no auth file

**tubeamp absorbs cookie rotations while running.** Google attaches refreshed
cookies (`SIDCC`, `__Secure-1PSIDCC`, sometimes `__Secure-*PSIDTS`) to many
responses. tubeamp merges those refreshes into the live session and writes them
back to the auth file atomically, so a session that started healthy keeps itself
alive instead of decaying from the moment you copied the cookie — extending how
long a sign-in lasts before you have to refresh the file by hand. The write-back
also merges whatever is currently in the file, so concurrent users of the same
auth file (say, a `tubeamp -status` in your tmux status bar next to the running
TUI) keep each other's rotations instead of overwriting them.

**The cookie-rotation gotcha.** A YouTube session cookie copied from an *active*
browser profile goes stale **within minutes**: Google continuously rotates the
`__Secure-*PSIDTS` cookies, and once your live browser rotates to a new one the
snapshot tubeamp imported is invalidated — tubeamp falls back to anonymous (which
is what the `○ anonymous` indicator is for). This — not the request or the
importer — is behind essentially every "I'm signed in but tubeamp says anonymous"
case: a cookie that authenticates the moment it's read can be dead a few minutes
later. If the rotation happens mid-session — sign-in succeeded at startup but a
later library load comes back logged out — the indicator downgrades to
`○ anonymous` on the spot instead of contradicting the failing loads.

**Auto-refresh from the browser.** When a session resolves anonymous and you
imported it with `-auth`, the TUI fires a one-shot re-import from the remembered
browser (`auth_browser`) — reading fresh cookies, rewriting the auth file, and
re-checking — at most once per session. On success the status line shows
`session refreshed from <browser>` and the indicator flips back to signed-in; on
failure it shows `re-import failed — run tubeamp -auth <browser>`. If the
re-import worked but the re-check could not run (offline), the fresh cookies are
kept and the status says `re-imported from <browser> — could not confirm
sign-in`. So as long as the browser is still logged in, a stale tubeamp session
usually heals itself.

The reliable manual trick is to copy the cookie from a **private /
incognito** window: log in there, grab the Cookie header, then **close the window
without logging out**. A closed incognito session is not rotated, so that cookie
keeps working far longer.

**Multiple Google accounts.** If you are logged into several Google accounts in
the same browser, the cookie alone is ambiguous; YouTube disambiguates with an
account index. Set it in `~/.config/tubeamp/config.yaml`:

```yaml
auth_user: 0   # 0 = first/default account, 1 = second, …
```

This maps to the `X-Goog-AuthUser` header. If the wrong account (or anonymous)
shows up despite a fresh cookie, try the next index.

`tubeamp -status` also reports the sign-in state on a `yt music:` line (bounded
to ~2s so it never stalls if the network is down).

## Themes

Built-ins: catppuccin-mocha, catppuccin-latte, gruvbox-dark, nord, dracula,
tokyo-night, cyberpunk (a neon magenta/cyan-on-deep-purple theme). cyberpunk is
the default. Drop your own YAML in `~/.config/tubeamp/themes/` and set
`theme: <name>` in `~/.config/tubeamp/config.yaml`, or pick live with `T`.

Pixel-art covers render in the artwork's own colors by default
(`art_palette: auto` in config.yaml). Set `art_palette: theme` to quantize
covers to the active theme's palette instead, so the art always matches the UI.

## Architecture

See `docs/CONTRACTS.md` for the package layout and public APIs.
Unix-only for now (mpv IPC uses a unix socket).
