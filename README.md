# tubeamp

A Lazygit-inspired terminal UI for YouTube Music, written in Go.

Stacked, numbered panels on the left (Library / Playlists / Queue), a contextual
main view on the right, a persistent player bar with pixel-art album covers, and
number-key panel switching with arrow-key navigation. Themeable via simple YAML files.
A **tubeamp** wordmark sits in the top-left, in the theme's accent colour (the
hue that highlights the active panel).

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
playlists on startup, Library → "Liked Songs" loads your Liked Songs, opening
a playlist loads its tracks, and the remaining Library sections are wired off
your library too (radio not yet) — though the derived Songs/Artists/Albums
sections need the **OAuth** sign-in specifically (see the section notes
below). The library is read through the official
**YouTube Data API** when you sign in with OAuth (durable, no cookie rotation),
or through cookies when you sign in that way; search and playback always use the
cookie/anonymous InnerTube client. An **anonymous** session keeps the demo/mock
library and toasts a sign-in hint.

The Library sections behave like this (all derived honestly from what the APIs
actually expose). Songs, Artists and Albums are built from a whole-library
aggregate that only the **OAuth** (Data API) session exposes — on a
cookie-signed-in session those three sections show demo data and say so
("needs the OAuth sign-in — run tubeamp -login"):

- **Liked Songs** — your Liked Songs (first pages today; full pagination is on).
- **Songs** — your *whole library* aggregated: Liked Songs plus every owned
  playlist's tracks, deduped. Derived from your library, not a separate API.
- **Artists** — that same aggregate grouped by each track's primary artist
  (`♪ <name> (<n>)`); `enter` drills into an artist's tracks. (The Data API can't
  reach YT Music's artist pages, so this grouping is the honest substitute.)
- **Albums** — the aggregate grouped by album. The Data API exposes neither a
  song's album nor its duration, so tubeamp fills those in via **anonymous
  InnerTube** lookups (a dedicated unauthenticated client — the per-song probes
  are never attributed to your account), cached permanently on disk. The list
  therefore **fills in
  progressively** the first time (watch "enriching albums… N/M" on the status
  line) and is instant on later visits. `enter`/`o` on an album opens it.
- **History** — tubeamp's **own local play history** (most-recent first), not
  YouTube's: Google removed watch-history from the public APIs, so this is just
  what you've played in tubeamp, recorded on disk.

Library tracks loaded over the Data API initially show no album column or
duration (the Data API does not expose them; the duration fills in from mpv once
a track plays, and the album/duration fill in permanently once enriched), and the
Data API library differs slightly in content from a cookie session — see the
OAuth caveats under [Signing in](#signing-in).

Time-synced lyrics are surfaced in a panel below the main view while a track
plays: LRC fetching/parsing from [LRCLIB](https://lrclib.net) with a YouTube
Music plain-text fallback. See [Lyrics](#lyrics).

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

Keys: `1/2/3/4` jump to a panel, `h/l` cycle panels (including the lyrics panel
while it is on screen), `j/k` move (`ctrl+d`/`ctrl+u` half-page), `enter` plays,
`space` pause, `n`/`p` next/previous, `←/→` seek ±5s, `↑/↓` or `+/-` volume, `/` search,
`o` open album — an album row, or the album the highlighted **song** belongs to
(works on song rows in search, your library, playlists, and the queue), `T` theme
picker, `?` help, `q` quit (playback keeps running in the background).

Search returns two sections — Songs and Albums. On an album row, `enter` plays the
whole album and `o` opens an album view (cover, metadata, track list) where `enter`
plays the album from the highlighted track and `esc` returns to the results. `o` on
a song row opens that song's album view too.

## Lyrics

While a track is playing, a lyrics panel appears in the right column, below the
main view and above the player bar.

- **Synced** lyrics (LRC) scroll automatically, with the current line highlighted
  and its neighbours dimmed, in step with playback (the panel marks itself
  `♪ synced`). Lyrics come from [LRCLIB](https://lrclib.net); when LRCLIB has no
  synced lyrics, tubeamp falls back to YouTube Music's **plain** (unsynced) text
  (`unsynced`). When nothing is found the panel shows "No lyrics found.".
- Lyrics are fetched in the background on each track change and cached per track,
  so replays and seeks never refetch. The panel hides automatically when nothing
  is playing or the terminal is too short to show it without crowding the main
  view.

The panel is **focusable while it is on screen**: `h`/`l` cycle onto it after the
main view (it has no number key, and the cycle skips it while hidden). With it
focused, `j`/`k`, `ctrl+u`/`ctrl+d` and `g`/`G` scroll the lyrics:

- **Unsynced** lyrics scroll line by line / by half-pages.
- **Synced** lyrics *peek-scroll*: scrolling pauses auto-follow (the panel marks
  itself `⏸ paused — esc to follow`) so you can read ahead or back. Auto-follow
  re-engages — snapping back to the live line — when you press `esc`, after a few
  seconds of no scrolling, or when you move focus away. While focused and still
  following, the panel marks itself `▶ following`.

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
but signing in unlocks results tied to your account. There are two ways to sign
in: **OAuth** (durable, recommended for a session that lasts months) and
**cookies** (imported from your browser, or copied by hand).

### OAuth (durable sign-in)

OAuth gives you a long-lived, auto-refreshing session — no cookie rotation, no
re-importing. Because Google revoked the shared OAuth credentials ytmusicapi
once bundled, you create your own Google OAuth client once. It is free and takes
a couple of minutes.

**One-time Google Cloud setup**

1. Open the [Google Cloud Console](https://console.cloud.google.com/) and create
   (or pick) a project.
2. Enable the **YouTube Data API v3** for that project
   (APIs & Services → Library → search "YouTube Data API v3" → Enable).
3. Configure the OAuth consent screen if prompted (External; you only need to add
   yourself as a test user).
4. APIs & Services → Credentials → **Create credentials → OAuth client ID**, and
   choose application type **"TVs and Limited Input devices"**.
5. Copy the generated **client ID** and **client secret** into
   `~/.config/tubeamp/config.yaml`:

   ```yaml
   oauth_client_id:     "XXXXXXXX.apps.googleusercontent.com"
   oauth_client_secret: "YYYYYYYY"
   ```

**Signing in**

```sh
tubeamp -login     # prints a URL + code; open it, enter the code, done
tubeamp -logout    # forget the OAuth token
```

`-login` prints a verification URL and a short user code. Open the URL in any
browser (phone is fine), enter the code, and approve. tubeamp polls in the
background and, once you approve, saves the token and prints `signed in as
<name>`. The token is stored at:

```
~/.local/share/tubeamp/oauth.json    # or $XDG_DATA_HOME/tubeamp/oauth.json
```

It **lasts months and refreshes itself automatically** — tubeamp swaps in a new
access token whenever the old one expires, with no action from you. On startup,
if `oauth.json` exists and the client credentials are configured, tubeamp uses
OAuth as the **library** source (the official YouTube Data API), in preference to
the cookie auth file. Search and playback still go through the cookie/anonymous
InnerTube client either way. `-logout` just deletes `oauth.json`.

If Google ever revokes the token (you removed the app's access, or the consent
screen's test-mode tokens expired), the TUI says **`sign-in expired — run
tubeamp -login`** on the status line and the header indicator drops out of the
signed-in state — one `tubeamp -login` signs you back in.
The browser cookie auto-refresh below never kicks in for an OAuth session.

What OAuth unlocks is durable **library** access — your Liked Songs and
playlists — without the cookie-rotation headache below. It powers the library
*only*, through the official YouTube **Data API v3**: search, album browsing,
lyrics and playback resolution all keep using the cookie/anonymous InnerTube
client (Google's private youtubei API rejects OAuth bearer tokens, so OAuth can
never drive it). A couple of caveats:

- OAuth does **not** change album-search ranking. The thin album-search results
  for some queries are an InnerTube quirk, already worked around by the
  song-derived album fallback; signing in (either way) does not affect it.
- The Data API has no equivalent of YT Music's library views, so the content
  differs slightly from a cookie session: "Liked Songs" is YouTube's
  **liked-videos** auto-playlist ("LL") — every video you ever liked, music or
  not — rather than YT Music's narrower Liked Music list, and the Playlists
  panel lists only playlists you **own** (playlists you saved/followed from
  other channels do not appear).
- The Data API does not expose a track's album or duration, so Liked Songs /
  playlist rows loaded over OAuth show an empty album column and a blank duration
  in the list (the duration fills in from mpv once the track plays).

### Cookies (browser import)

Cookie auth stores a single cookie header in a plain-text file:

```
~/.local/share/tubeamp/auth          # or $XDG_DATA_HOME/tubeamp/auth
```

**Import from your browser (one command)**

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

The header row (right-aligned, beside the wordmark) then shows your live sign-in
state:

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
usually heals itself. This applies to **cookie sessions only**: while you are
signed in via OAuth the re-import never fires (a remembered `auth_browser` from
an earlier cookie setup will not silently replace your OAuth session with
cookies — a dead OAuth session prompts `tubeamp -login` instead).

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
