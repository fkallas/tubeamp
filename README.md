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
(ytmusicapi behaves identically). Song search is unaffected, so tubeamp works
around it: every song result carries its album's browseId, so the Albums section
is reconstructed from the (full-catalog) song hits and shown first, with the
degraded album-vertical results merged in after and deduped. Major-label albums
therefore show up in search even anonymously. Signing in is supported (see
[Signing in](#signing-in)) and tubeamp now shows your live sign-in state in the
header; be aware that cookies copied from a logged-in browser frequently resolve
as **anonymous** anyway, because Google rotates them within hours (see the
cookie-rotation note below).

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
command — tubeamp reads the sign-in cookies straight out of the browser's cookie
store and writes the auth file for you:

```sh
tubeamp -auth chrome     # or: chromium, edge, brave, firefox, safari
tubeamp -auth            # bare -auth = "auto": try every supported browser
```

> **Quit the browser first.** The single most common reason a signed-in user
> imports cookies that come back **anonymous** is that the browser is still
> running: a fresh login lives in the browser's memory and the SQLite
> write-ahead log, while tubeamp can only read the *on-disk snapshot*, which is
> stale. **Fully quit the browser** (on macOS, Cmd-Q — not just closing the
> window) before running `tubeamp -auth`. If you see the anonymous result and
> the browser is still open, tubeamp now tells you exactly this and to retry
> after quitting.

It then confirms with the live API and prints `signed in as <name>` (or, if the
cookies still resolve logged out, the anonymous advice — `imported, but YouTube
still resolved anonymous — are you logged into <browser>? Try signing in there,
or import from another browser.`, or, when the browser is **running**,
`<browser> is running — cookies copied from a running browser are often stale.
Quit <browser> completely (Cmd-Q) and run 'tubeamp -auth <browser>' again.`; if
the confirmation itself could not run — offline, timeout — it says so instead of
blaming the cookies, and you can check later with `tubeamp -status`). A bare
`tubeamp -auth` (auto) is attributed to the browser whose cookie store actually
supplied the session, so these messages — including the running-browser check —
name that concrete browser, never "auto". The browser
the cookies came from is remembered in `config.yaml` (`auth_browser`), and the TUI
uses it to silently re-import a session that has gone stale (see the rotation note
below). A running browser does not *block* the read — tubeamp reads through a
temporary copy of the (locked) cookie database — but, as above, that copy can be
stale, so quitting first is what makes the import reliable.

**Multiple profiles.** A browser with several profiles (e.g. Firefox's
`*.default` beside the active `*.default-release`) imports from the profile that
actually holds your Google login: tubeamp picks the store whose cookie DB carries
a SAPISID, preferring the profile marked default in `profiles.ini`, so a stale or
empty secondary profile never wins.

- **macOS Keychain prompt.** Chrome-family cookies (Chrome/Chromium/Edge/Brave)
  are encrypted with a key kept in your login Keychain, so the first Chrome
  import pops a Keychain consent dialog — allow it. Deny it and tubeamp reports a
  clear error rather than a cryptic decryption failure.
- **App-Bound Encryption caveat.** Very recent Chrome releases wrap the cookie
  key in app-bound encryption that refuses external reads. If `-auth chrome`
  fails to decrypt, use **Firefox** (`-auth firefox`) or **Safari**
  (`-auth safari`) — neither store is Keychain-encrypted — or fall back to the
  manual method below.

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

**The cookie-rotation gotcha.** Cookies copied from an *active* browser profile
go stale within hours: Google continuously rotates the `__Secure-*PSIDTS`
cookies, and once the browser rotates them your copied snapshot is invalidated —
tubeamp silently falls back to anonymous (which is exactly what the `○ anonymous`
indicator is for). If the rotation happens mid-session — sign-in succeeded at
startup but a later library load comes back logged out — the indicator
downgrades to `○ anonymous` on the spot instead of contradicting the failing
loads.

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
