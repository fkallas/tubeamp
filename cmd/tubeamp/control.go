package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/player"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// controlFlags holds the one-shot CLI control flags. When any is set, tubeamp
// drives the already-running daemon instead of opening the TUI.
type controlFlags struct {
	pause  bool
	next   bool
	prev   bool
	stop   bool
	status bool
	line   bool
	queue  bool
	kill   bool
	vol    string // "" unset; "N" absolute, "+N"/"-N" relative
	seek   string // "" unset; "±SECONDS" relative
}

// any reports whether any control flag is set (=> CLI mode, not the TUI).
func (f controlFlags) any() bool {
	return f.pause || f.next || f.prev || f.stop || f.status || f.line ||
		f.queue || f.kill || f.vol != "" || f.seek != ""
}

// dispatchControl runs the requested control command against the running daemon
// and returns a process exit code. It attaches only (never spawns). When no
// daemon is running it prints "tubeamp: not running" to errOut and returns 1 —
// except -line, which stays silent and returns 0 so it can feed tmux/status
// scripts without noise.
func dispatchControl(out, errOut io.Writer, cfg *config.Config, f controlFlags) int {
	p, err := player.New(player.Options{
		MPVPath:    cfg.MPVPath,
		YTDLFormat: cfg.YTDLFormat,
		Volume:     cfg.Volume,
		AttachOnly: true,
	})
	if err != nil {
		if f.line {
			return 0 // silent empty success for scripts
		}
		fmt.Fprintln(errOut, "tubeamp: not running")
		return 1
	}

	// -kill terminates the daemon (and removes the socket/lock); Quit detaches
	// for us, so there is no Close to defer.
	if f.kill {
		if err := p.Quit(); err != nil {
			fmt.Fprintln(errOut, "tubeamp:", err)
			return 1
		}
		return 0
	}
	defer p.Close() // detach; playback keeps running

	switch {
	case f.stop:
		return runErr(errOut, p.Stop())
	case f.pause:
		return runErr(errOut, p.TogglePause())
	case f.next:
		return runErr(errOut, p.Next())
	case f.prev:
		return runErr(errOut, p.Prev())
	case f.vol != "":
		return applyVolume(errOut, p, f.vol)
	case f.seek != "":
		return applySeek(errOut, p, f.seek)
	case f.status:
		return printStatus(out, errOut, cfg, p)
	case f.line:
		return printLine(out, p)
	case f.queue:
		return printQueue(out, errOut, p)
	}
	return 0
}

// runErr reports an action error on errOut and maps it to an exit code.
func runErr(errOut io.Writer, err error) int {
	if err != nil {
		fmt.Fprintln(errOut, "tubeamp:", err)
		return 1
	}
	return 0
}

// applyVolume sets an absolute (N) or relative (+N/-N) volume.
func applyVolume(errOut io.Writer, p *player.Player, spec string) int {
	n, err := strconv.Atoi(spec)
	if err != nil {
		fmt.Fprintf(errOut, "tubeamp: invalid volume %q\n", spec)
		return 1
	}
	target := n
	if strings.HasPrefix(spec, "+") || strings.HasPrefix(spec, "-") {
		s, serr := p.Snapshot()
		if serr != nil {
			fmt.Fprintln(errOut, "tubeamp:", serr)
			return 1
		}
		target = s.Volume + n
	}
	return runErr(errOut, p.SetVolume(target))
}

// applySeek seeks by a relative number of seconds (e.g. +10, -10, 30).
func applySeek(errOut io.Writer, p *player.Player, spec string) int {
	sec, err := strconv.ParseFloat(spec, 64)
	if err != nil {
		fmt.Fprintf(errOut, "tubeamp: invalid seek %q\n", spec)
		return 1
	}
	return runErr(errOut, p.Seek(sec))
}

// printStatus writes a multi-line human-readable status. A best-effort YT Music
// sign-in line is appended last (bounded so -status never hangs on a dead
// network).
func printStatus(out, errOut io.Writer, cfg *config.Config, p *player.Player) int {
	s, err := p.Snapshot()
	if err != nil {
		fmt.Fprintln(errOut, "tubeamp:", err)
		return 1
	}
	if s.PlaylistPos < 0 || s.PlaylistPos >= len(s.Tracks) {
		fmt.Fprintln(out, "■ stopped")
		fmt.Fprintln(out, ytmStatusLine(cfg))
		return 0
	}
	t := s.Tracks[s.PlaylistPos]
	glyph := "▶"
	if s.Paused {
		glyph = "⏸"
	}
	fmt.Fprintf(out, "%s %s\n", glyph, t.Title)
	if al := t.ArtistLine(); al != "" {
		fmt.Fprintf(out, "  artist: %s\n", al)
	}
	if t.Album != "" {
		fmt.Fprintf(out, "  album:  %s\n", t.Album)
	}
	fmt.Fprintf(out, "  time:   %s / %s\n", fmtSecs(s.TimePos), fmtSecs(s.Duration))
	vol := fmt.Sprintf("%d%%", s.Volume)
	if s.Mute {
		vol += " (muted)"
	}
	fmt.Fprintf(out, "  volume: %s\n", vol)
	fmt.Fprintf(out, "  track:  %d/%d\n", s.PlaylistPos+1, len(s.Tracks))
	fmt.Fprintln(out, ytmStatusLine(cfg))
	return 0
}

// ytmAccountInfo probes the YT Music sign-in state used by -status. It attaches
// a throwaway InnerTube client (auth from DataDir()/auth, account index from
// config) and asks for the account info. It is a package variable so tests can
// stub it without touching the network.
var ytmAccountInfo = func(ctx context.Context, cfg *config.Config) (name string, signedIn bool, err error) {
	auth, _ := ytm.LoadAuth(filepath.Join(config.DataDir(), "auth"))
	c := ytm.NewClient(auth)
	c.SetAuthUser(cfg.AuthUser)
	return c.AccountInfo(ctx)
}

// ytmStatusLine returns the "yt music: …" status line for -status under a tight
// 2s timeout: a signed-in cookie yields "signed in as <name>", a logged-out or
// absent cookie yields "anonymous", and any error (e.g. the network is down)
// yields "unknown" — so -status never fails or stalls on this lookup.
func ytmStatusLine(cfg *config.Config) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	name, signedIn, err := ytmAccountInfo(ctx, cfg)
	switch {
	case err != nil:
		return "yt music: unknown"
	case signedIn:
		return "yt music: signed in as " + name
	default:
		return "yt music: anonymous"
	}
}

// printLine writes one compact status line (≈48 cols), or nothing when idle so
// the caller's tmux/status bar shows an empty cell.
func printLine(out io.Writer, p *player.Player) int {
	s, err := p.Snapshot()
	if err != nil {
		return 0 // silent
	}
	if s.PlaylistPos < 0 || s.PlaylistPos >= len(s.Tracks) {
		return 0 // idle => empty line
	}
	t := s.Tracks[s.PlaylistPos]
	glyph := "♪"
	if s.Paused {
		glyph = "⏸"
	}
	text := glyph + " " + t.Title
	if al := t.ArtistLine(); al != "" {
		text += " — " + al
	}
	timePart := fmtSecs(s.TimePos) + "/" + fmtSecs(s.Duration)
	const maxCols = 48
	budget := maxCols - len([]rune(timePart)) - 1
	fmt.Fprintln(out, truncateRunes(text, budget)+" "+timePart)
	return 0
}

// printQueue writes the numbered queue, marking the playing row with ▶.
func printQueue(out, errOut io.Writer, p *player.Player) int {
	s, err := p.Snapshot()
	if err != nil {
		fmt.Fprintln(errOut, "tubeamp:", err)
		return 1
	}
	if len(s.Tracks) == 0 {
		fmt.Fprintln(out, "queue empty")
		return 0
	}
	for i, t := range s.Tracks {
		marker := "  "
		if i == s.PlaylistPos {
			marker = "▶ "
		}
		line := fmt.Sprintf("%s%3d. %s", marker, i+1, t.Title)
		if al := t.ArtistLine(); al != "" {
			line += " — " + al
		}
		fmt.Fprintln(out, line)
	}
	return 0
}

// fmtSecs renders seconds as "m:ss".
func fmtSecs(sec float64) string {
	if sec < 0 || math.IsNaN(sec) || math.IsInf(sec, 0) {
		sec = 0
	}
	s := int(sec)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// truncateRunes clips s to at most n runes, appending an ellipsis when cut.
func truncateRunes(s string, n int) string {
	if n < 1 {
		n = 1
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
