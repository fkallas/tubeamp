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

Note: anonymous InnerTube **album** search only surfaces self-distributed
releases — YouTube withholds the major-label catalog from logged-out clients
(ytmusicapi behaves identically). Song search is unaffected. Signing in is
supported (see [Signing in](#signing-in)) and tubeamp now shows your live
sign-in state in the header; be aware that cookies copied from a logged-in
browser frequently resolve as **anonymous** anyway, because Google rotates them
within hours (see the cookie-rotation note below).

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

Keys: `1/2/3/4` jump to a panel, `h/l` cycle panels, `j/k` move, `enter` plays,
`space` pause, `n`/`p` next/previous, `←/→` seek ±5s, `↑/↓` or `+/-` volume, `/` search,
`o` open the selected album (album rows in search results), `T` theme picker,
`?` help, `q` quit (playback keeps running in the background).

Search returns two sections — Songs and Albums. On an album row, `enter` plays the
whole album and `o` opens an album view (cover, metadata, track list) where `enter`
plays the album from the highlighted track and `esc` returns to the results.

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

**Getting the Cookie header**

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

**The cookie-rotation gotcha.** Cookies copied from an *active* browser profile
go stale within hours: Google continuously rotates the `__Secure-*PSIDTS`
cookies, and once the browser rotates them your copied snapshot is invalidated —
tubeamp silently falls back to anonymous (which is exactly what the `○ anonymous`
indicator is for). The reliable trick is to copy the cookie from a **private /
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
tokyo-night. Drop your own YAML in `~/.config/tubeamp/themes/` and set
`theme: <name>` in `~/.config/tubeamp/config.yaml`, or pick live with `T`.

Pixel-art covers render in the artwork's own colors by default
(`art_palette: auto` in config.yaml). Set `art_palette: theme` to quantize
covers to the active theme's palette instead, so the art always matches the UI.

## Architecture

See `docs/CONTRACTS.md` for the package layout and public APIs.
Unix-only for now (mpv IPC uses a unix socket).
