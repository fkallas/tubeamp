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
renderer work end to end with mock library data; the YT Music API client wires
unauthenticated song/album search and album browsing into the search view, with
library/playlists/radio not yet.

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

Keys: `1/2/3/4` jump to a panel, `↑/↓` move, `enter` plays,
`space` pause, `n`/`p` next/previous, `←/→` seek ±5s, `+/-` volume, `/` search,
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
| `tubeamp -status`  | human-readable status (title, artists, album, position, volume)|
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

## Themes

Built-ins: catppuccin-mocha, catppuccin-latte, gruvbox-dark, nord, dracula,
tokyo-night. Drop your own YAML in `~/.config/tubeamp/themes/` and set
`theme: <name>` in `~/.config/tubeamp/config.yaml`, or pick live with `T`.

## Architecture

See `docs/CONTRACTS.md` for the package layout and public APIs.
Unix-only for now (mpv IPC uses a unix socket).
