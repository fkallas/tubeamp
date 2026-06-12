// Command tubeamp is a Lazygit-inspired terminal UI for YouTube Music. It wires
// the configuration, theme, mpv player, InnerTube client, and play queue into
// the Bubble Tea UI and runs it in the alternate screen buffer.
//
// Missing optional pieces degrade gracefully rather than aborting: if mpv cannot
// be started the UI runs with playback disabled (a status-line notice), and if
// the auth file is absent the InnerTube client runs unauthenticated (search
// still works).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/core"
	"github.com/fkallas/tubeamp/internal/player"
	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ui"
	"github.com/fkallas/tubeamp/internal/ytm"
)

// version is the tubeamp build version. Overridable at build time via
// -ldflags "-X main.version=...".
var version = "0.1.0"

func main() {
	themeFlag := flag.String("theme", "", "theme name to use (overrides config)")
	versionFlag := flag.Bool("version", false, "print version and exit")

	// One-shot control flags: when any is set, drive the running daemon instead
	// of opening the TUI. They attach only and never spawn mpv.
	pauseFlag := flag.Bool("p", false, "toggle pause on the running daemon")
	nextFlag := flag.Bool("next", false, "skip to the next track")
	prevFlag := flag.Bool("prev", false, "skip to the previous track")
	stopFlag := flag.Bool("stop", false, "stop playback")
	statusFlag := flag.Bool("status", false, "print human-readable playback status")
	lineFlag := flag.Bool("line", false, "print one compact status line (empty when idle)")
	queueFlag := flag.Bool("queue", false, "print the numbered queue")
	killFlag := flag.Bool("kill", false, "quit the background mpv daemon")
	volFlag := flag.String("vol", "", "set volume: N, +N, or -N")
	seekFlag := flag.String("seek", "", "seek by ±SECONDS (relative)")

	// -auth imports a sign-in by reading cookies from a browser. It accepts a
	// browser name (-auth chrome / -auth=chrome) or stands alone (-auth) for
	// "auto" (try every supported browser).
	var authFlag authBrowserFlag
	flag.Var(&authFlag, "auth", "import sign-in cookies from a browser (chrome/chromium/edge/brave/firefox/safari; bare -auth = auto)")

	// -login / -logout drive the durable OAuth device-flow sign-in (requires
	// oauth_client_id/secret in config). They run and exit like -auth.
	loginFlag := flag.Bool("login", false, "sign in via the OAuth device flow (durable, auto-refreshing)")
	logoutFlag := flag.Bool("logout", false, "sign out of OAuth (delete the stored token)")

	flag.Parse()

	if *versionFlag {
		fmt.Printf("tubeamp %s\n", version)
		return
	}

	// Configuration: a missing file yields defaults, not an error.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tubeamp:", err)
		os.Exit(1)
	}

	// -auth runs the one-command browser import and exits (it does not open the
	// TUI or touch the daemon).
	if authFlag.set {
		os.Exit(runAuthImport(os.Stdout, os.Stderr, cfg, authBrowser(&authFlag)))
	}

	// -login / -logout run the OAuth device flow and exit.
	if *loginFlag {
		os.Exit(runLogin(os.Stdout, os.Stderr, cfg))
	}
	if *logoutFlag {
		os.Exit(runLogout(os.Stdout, os.Stderr))
	}

	cf := controlFlags{
		pause: *pauseFlag, next: *nextFlag, prev: *prevFlag, stop: *stopFlag,
		status: *statusFlag, line: *lineFlag, queue: *queueFlag, kill: *killFlag,
		vol: *volFlag, seek: *seekFlag,
	}
	if cf.any() {
		os.Exit(dispatchControl(os.Stdout, os.Stderr, cfg, cf))
	}

	if err := run(cfg, *themeFlag); err != nil {
		fmt.Fprintln(os.Stderr, "tubeamp:", err)
		os.Exit(1)
	}
}

// run builds the application graph and runs the UI. Optional subsystems degrade
// to nil rather than aborting. On exit the player is detached (Close), NOT
// killed — mpv keeps playing in the background so playback survives the TUI.
func run(cfg *config.Config, themeOverride string) error {
	// Theme: -theme overrides the configured theme; on any load failure we fall
	// back to the built-in default with a warning.
	themeName := cfg.Theme
	if themeOverride != "" {
		themeName = themeOverride
		cfg.Theme = themeOverride
	}
	th, err := theme.Load(themeName, config.ThemesDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tubeamp: theme %q unavailable (%v); using default\n", themeName, err)
		th = theme.Default()
		cfg.Theme = th.Name
	}

	// Player: attach to a running daemon or spawn a detached one. If mpv is
	// missing or never connects, run with a nil player; the UI surfaces a
	// "playback disabled" notice on its status line.
	var p *player.Player
	if pl, perr := player.New(player.Options{
		MPVPath:    cfg.MPVPath,
		YTDLFormat: cfg.YTDLFormat,
		Volume:     cfg.Volume,
	}); perr == nil {
		p = pl
	}
	// Detach (not kill) on the way out so playback continues in the background.
	if p != nil {
		defer p.Close()
	}

	// InnerTube client (search + playback resolution). It is cookie-authenticated
	// when an auth file is present, else anonymous — NEVER OAuth: Google's
	// youtubei API rejects OAuth bearer tokens. A missing/unreadable cookie file
	// simply means unauthenticated (search still works).
	client := buildClient(cfg)
	client.SetAuthUser(cfg.AuthUser)

	q := core.NewQueue()

	m := ui.New(cfg, th, p, client, q)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("running ui: %w", err)
	}
	return nil
}

// buildClient constructs the InnerTube client for the TUI. InnerTube does not
// accept OAuth bearer tokens (Google's youtubei API rejects them), so this
// client is cookie-authenticated when an auth file is present, else anonymous —
// never OAuth. OAuth instead powers the Data API library client (see commit
// wiring in run/ui). A missing/unreadable cookie file yields an unauthenticated
// client (search still works).
func buildClient(cfg *config.Config) *ytm.Client {
	var auth *ytm.Auth
	if a, aerr := ytm.LoadAuth(filepath.Join(config.DataDir(), "auth")); aerr == nil {
		auth = a
	}
	return ytm.NewClient(auth)
}
