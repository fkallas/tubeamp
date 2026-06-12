# tubeamp

A Lazygit-inspired terminal UI for YouTube Music, written in Go.

Stacked, numbered panels on the left (Library / Playlists / Queue), a contextual
main view on the right, a persistent player bar with pixel-art album covers, and
number-key panel switching with arrow-key navigation. Themeable via simple YAML files.

## Status

Early scaffold. The TUI shell, theming, mpv playback pipeline, and pixel-art
renderer work end to end with mock library data; the YT Music API client is a
skeleton (unauthenticated search wired, library/playlists/radio not yet).

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
`T` theme picker, `?` help, `q` quit.

## Themes

Built-ins: catppuccin-mocha, catppuccin-latte, gruvbox-dark, nord, dracula,
tokyo-night. Drop your own YAML in `~/.config/tubeamp/themes/` and set
`theme: <name>` in `~/.config/tubeamp/config.yaml`, or pick live with `T`.

## Architecture

See `docs/CONTRACTS.md` for the package layout and public APIs.
Unix-only for now (mpv IPC uses a unix socket).
