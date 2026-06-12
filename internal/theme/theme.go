// Package theme provides builtin and user-defined colour themes for tubeamp,
// plus lipgloss style helpers so no other package hard-codes colours.
package theme

import (
	"embed"
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// Colors holds all nine semantic colour tokens as "#rrggbb" hex strings.
type Colors struct {
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

// Theme pairs a display name with its colour tokens.
type Theme struct {
	Name   string `yaml:"name"`
	Colors Colors `yaml:"colors"`
}

// hardcodedMocha is the last-resort fallback if the embedded YAML is somehow
// missing or corrupt. It must never be reached in a correctly built binary.
var hardcodedMocha = &Theme{
	Name: "catppuccin-mocha",
	Colors: Colors{
		BorderInactive: "#45475a",
		BorderActive:   "#cba6f7",
		TextPrimary:    "#cdd6f4",
		TextMuted:      "#6c7086",
		Accent:         "#cba6f7",
		Playing:        "#a6e3a1",
		Error:          "#f38ba8",
		SelectionBG:    "#313244",
		SelectionFG:    "#cdd6f4",
	},
}

// builtinNames is the canonical sorted list of builtin theme names.
var builtinNames = []string{
	"catppuccin-latte",
	"catppuccin-mocha",
	"cyberpunk",
	"dracula",
	"gruvbox-dark",
	"nord",
	"tokyo-night",
}

// Default returns the catppuccin-mocha theme. It loads from the embedded YAML
// and falls back to a hardcoded struct if the embed is unavailable.
func Default() *Theme {
	t, err := loadBuiltin("catppuccin-mocha")
	if err != nil {
		// Embedded file is corrupt or missing — return the hardcoded fallback.
		return hardcodedMocha
	}
	return t
}

// Builtins returns a sorted slice of the builtin theme names, read directly
// from the embedded filesystem so the list stays in sync with actual files.
func Builtins() []string {
	return listBuiltinFromFS()
}

// List returns the deduplicated, sorted union of builtin theme names and any
// *.yaml files found in userDir. User themes shadow builtins of the same name.
// If userDir is empty or unreadable the result contains only builtins.
func List(userDir string) []string {
	seen := make(map[string]struct{}, len(builtinNames))
	for _, n := range builtinNames {
		seen[n] = struct{}{}
	}

	if userDir != "" {
		entries, err := os.ReadDir(userDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if strings.ToLower(filepath.Ext(e.Name())) == ".yaml" {
					name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
					seen[name] = struct{}{}
				}
			}
		}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Load loads a theme by name. It checks userDir/<name>.yaml first, then the
// embedded builtins. If the name is not found, the returned error lists all
// available theme names.
func Load(name, userDir string) (*Theme, error) {
	// Try user directory first.
	if userDir != "" {
		p := filepath.Join(userDir, name+".yaml")
		data, err := os.ReadFile(p)
		if err == nil {
			t, parseErr := parseTheme(data)
			if parseErr != nil {
				return nil, fmt.Errorf("theme: parsing %s: %w", p, parseErr)
			}
			return t, nil
		}
	}

	// Try builtin.
	t, err := loadBuiltin(name)
	if err == nil {
		return t, nil
	}

	// Not found anywhere — report available names.
	available := List(userDir)
	return nil, fmt.Errorf("theme: unknown theme %q; available: %s",
		name, strings.Join(available, ", "))
}

// loadBuiltin reads a builtin theme from the embedded filesystem.
func loadBuiltin(name string) (*Theme, error) {
	data, err := builtinFS.ReadFile("builtin/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("theme: builtin %q not found: %w", name, err)
	}
	t, err := parseTheme(data)
	if err != nil {
		return nil, fmt.Errorf("theme: parsing builtin %q: %w", name, err)
	}
	return t, nil
}

// parseTheme unmarshals YAML bytes into a Theme.
func parseTheme(data []byte) (*Theme, error) {
	var t Theme
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// --- Lipgloss style helpers -------------------------------------------------

// PanelBorder returns a rounded-border style coloured with the active or
// inactive border token.
func (t *Theme) PanelBorder(active bool) lipgloss.Style {
	col := t.Colors.BorderInactive
	if active {
		col = t.Colors.BorderActive
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(col))
}

// Title returns a style for panel title text. Active titles use the accent
// colour; inactive titles use the muted colour.
func (t *Theme) Title(active bool) lipgloss.Style {
	col := t.Colors.TextMuted
	if active {
		col = t.Colors.Accent
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(col))
}

// Primary returns a style using the primary text colour.
func (t *Theme) Primary() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.Colors.TextPrimary))
}

// Muted returns a style using the muted text colour.
func (t *Theme) Muted() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.Colors.TextMuted))
}

// AccentStyle returns a style using the accent colour.
func (t *Theme) AccentStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.Colors.Accent))
}

// PlayingStyle returns a style using the playing (green) colour.
func (t *Theme) PlayingStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.Colors.Playing))
}

// ErrorStyle returns a style using the error (red) colour.
func (t *Theme) ErrorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.Colors.Error))
}

// Selected returns a style with SelectionFG foreground on a SelectionBG
// background, used for highlighted list rows.
func (t *Theme) Selected() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Colors.SelectionFG)).
		Background(lipgloss.Color(t.Colors.SelectionBG))
}

// Palette parses all nine colour tokens into image/color values suitable for
// art quantization. Tokens that are not valid "#rrggbb" strings are skipped.
func (t *Theme) Palette() []color.Color {
	hexes := []string{
		t.Colors.BorderInactive,
		t.Colors.BorderActive,
		t.Colors.TextPrimary,
		t.Colors.TextMuted,
		t.Colors.Accent,
		t.Colors.Playing,
		t.Colors.Error,
		t.Colors.SelectionBG,
		t.Colors.SelectionFG,
	}
	out := make([]color.Color, 0, len(hexes))
	for _, h := range hexes {
		if c, ok := parseHexColor(h); ok {
			out = append(out, c)
		}
	}
	return out
}

// parseHexColor converts a "#rrggbb" string to an image/color.RGBA value.
func parseHexColor(s string) (color.Color, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return nil, false
	}
	r, err1 := strconv.ParseUint(s[0:2], 16, 8)
	g, err2 := strconv.ParseUint(s[2:4], 16, 8)
	b, err3 := strconv.ParseUint(s[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, false
	}
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xff}, true
}

// listBuiltinFromFS returns all builtin names by reading the embedded FS.
// Used internally so Builtins() is always in sync with actual embedded files.
func listBuiltinFromFS() []string {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return builtinNames // fallback to hardcoded list
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.ToLower(filepath.Ext(e.Name())) == ".yaml" {
			names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	sort.Strings(names)
	return names
}
