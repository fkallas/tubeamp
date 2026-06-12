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
	flag.Parse()

	if *versionFlag {
		fmt.Printf("tubeamp %s\n", version)
		return
	}

	if err := run(*themeFlag); err != nil {
		fmt.Fprintln(os.Stderr, "tubeamp:", err)
		os.Exit(1)
	}
}

// run builds the application graph and runs the UI. Optional subsystems degrade
// to nil rather than aborting; mpv is always closed before returning.
func run(themeOverride string) error {
	// Configuration: a missing file yields defaults, not an error.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

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

	// Player: if mpv is missing or never connects, run with a nil player; the UI
	// surfaces a "playback disabled" notice on its status line.
	var p *player.Player
	if pl, perr := player.New(player.Options{
		MPVPath:    cfg.MPVPath,
		YTDLFormat: cfg.YTDLFormat,
		Volume:     cfg.Volume,
	}); perr == nil {
		p = pl
	}
	// Always shut mpv down on the way out, including error paths below.
	if p != nil {
		defer p.Close()
	}

	// Auth: a missing or unreadable auth file simply means unauthenticated. The
	// client is still constructed (search works without credentials).
	var auth *ytm.Auth
	if a, aerr := ytm.LoadAuth(filepath.Join(config.DataDir(), "auth")); aerr == nil {
		auth = a
	}
	client := ytm.NewClient(auth)

	q := core.NewQueue()

	m := ui.New(cfg, th, p, client, q)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("running ui: %w", err)
	}
	return nil
}
