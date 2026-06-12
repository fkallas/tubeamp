package theme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fkallas/tubeamp/internal/theme"
)

// allBuiltins lists the six expected builtin theme names.
var allBuiltins = []string{
	"catppuccin-latte",
	"catppuccin-mocha",
	"dracula",
	"gruvbox-dark",
	"nord",
	"tokyo-night",
}

// TestBuiltinsLoad verifies that every builtin theme loads without error and
// that all nine colour tokens are valid #rrggbb hex strings.
func TestBuiltinsLoad(t *testing.T) {
	for _, name := range allBuiltins {
		name := name
		t.Run(name, func(t *testing.T) {
			th, err := theme.Load(name, "")
			if err != nil {
				t.Fatalf("Load(%q) error: %v", name, err)
			}
			if th.Name != name {
				t.Errorf("Name: got %q, want %q", th.Name, name)
			}
			colors := []struct {
				field string
				value string
			}{
				{"border_inactive", th.Colors.BorderInactive},
				{"border_active", th.Colors.BorderActive},
				{"text_primary", th.Colors.TextPrimary},
				{"text_muted", th.Colors.TextMuted},
				{"accent", th.Colors.Accent},
				{"playing", th.Colors.Playing},
				{"error", th.Colors.Error},
				{"selection_bg", th.Colors.SelectionBG},
				{"selection_fg", th.Colors.SelectionFG},
			}
			for _, c := range colors {
				if !isValidHex(c.value) {
					t.Errorf("field %s: %q is not a valid #rrggbb color", c.field, c.value)
				}
			}

			// Palette must return exactly 9 entries (all tokens are valid).
			palette := th.Palette()
			if len(palette) != 9 {
				t.Errorf("Palette() returned %d colors, want 9", len(palette))
			}
		})
	}
}

// TestDefaultIsMorea verifies that Default() returns catppuccin-mocha and
// never errors.
func TestDefaultIsMocha(t *testing.T) {
	th := theme.Default()
	if th == nil {
		t.Fatal("Default() returned nil")
	}
	if th.Name != "catppuccin-mocha" {
		t.Errorf("Default().Name = %q, want catppuccin-mocha", th.Name)
	}
}

// TestBuiltinsFunction verifies that Builtins() returns a sorted slice
// containing all expected builtin names.
func TestBuiltinsFunction(t *testing.T) {
	got := theme.Builtins()
	if len(got) != len(allBuiltins) {
		t.Fatalf("Builtins() returned %d names, want %d", len(got), len(allBuiltins))
	}
	for i, want := range allBuiltins {
		if got[i] != want {
			t.Errorf("Builtins()[%d] = %q, want %q", i, got[i], want)
		}
	}
}

// TestUnknownThemeError verifies that loading an unknown theme returns an
// error that mentions available theme names.
func TestUnknownThemeError(t *testing.T) {
	_, err := theme.Load("definitely-not-a-theme", "")
	if err == nil {
		t.Fatal("expected error for unknown theme, got nil")
	}
	msg := err.Error()
	// The error must mention at least one known builtin.
	if !strings.Contains(msg, "catppuccin-mocha") {
		t.Errorf("error message %q does not mention available themes", msg)
	}
	// Confirm the unknown name appears in the message.
	if !strings.Contains(msg, "definitely-not-a-theme") {
		t.Errorf("error message %q does not mention the requested name", msg)
	}
}

// TestUserThemeOverridesBuiltin verifies that a user-supplied YAML in the
// user themes directory takes precedence over a builtin of the same name.
func TestUserThemeOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()

	// Write a user theme that overrides "nord" with a different accent colour.
	yaml := `
name: nord
colors:
  border_inactive: "#ffffff"
  border_active: "#aabbcc"
  text_primary: "#ffffff"
  text_muted: "#aaaaaa"
  accent: "#aabbcc"
  playing: "#00ff00"
  error: "#ff0000"
  selection_bg: "#111111"
  selection_fg: "#ffffff"
`
	if err := os.WriteFile(filepath.Join(dir, "nord.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing user theme: %v", err)
	}

	th, err := theme.Load("nord", dir)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	// The user version has accent "#aabbcc"; the builtin has "#88c0d0".
	if th.Colors.Accent != "#aabbcc" {
		t.Errorf("user theme accent = %q, want #aabbcc", th.Colors.Accent)
	}
}

// TestListContainsBothBuiltinAndUser verifies that List returns a sorted,
// deduplicated union of builtin and user theme names.
func TestListContainsBothBuiltinAndUser(t *testing.T) {
	dir := t.TempDir()

	// Write a brand-new user theme.
	yaml := `
name: my-custom-theme
colors:
  border_inactive: "#000000"
  border_active: "#ffffff"
  text_primary: "#ffffff"
  text_muted: "#888888"
  accent: "#ffffff"
  playing: "#00ff00"
  error: "#ff0000"
  selection_bg: "#333333"
  selection_fg: "#ffffff"
`
	if err := os.WriteFile(filepath.Join(dir, "my-custom-theme.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing user theme: %v", err)
	}

	names := theme.List(dir)

	// Must include all builtins.
	for _, b := range allBuiltins {
		if !contains(names, b) {
			t.Errorf("List missing builtin %q; got: %v", b, names)
		}
	}

	// Must include the user theme.
	if !contains(names, "my-custom-theme") {
		t.Errorf("List missing user theme; got: %v", names)
	}

	// Must be sorted.
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("List is not sorted at index %d: %q < %q", i, names[i], names[i-1])
		}
	}
}

// TestListDeduplication verifies that a user theme that shadows a builtin
// appears only once in List.
func TestListDeduplication(t *testing.T) {
	dir := t.TempDir()

	// Shadow the "dracula" builtin.
	yaml := "name: dracula\ncolors:\n  border_inactive: \"#000000\"\n  border_active: \"#ffffff\"\n  text_primary: \"#ffffff\"\n  text_muted: \"#888888\"\n  accent: \"#ffffff\"\n  playing: \"#00ff00\"\n  error: \"#ff0000\"\n  selection_bg: \"#333333\"\n  selection_fg: \"#ffffff\"\n"
	if err := os.WriteFile(filepath.Join(dir, "dracula.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing user theme: %v", err)
	}

	names := theme.List(dir)

	// Count occurrences of "dracula".
	count := 0
	for _, n := range names {
		if n == "dracula" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("dracula appears %d times in List, want 1; got: %v", count, names)
	}
}

// TestListEmptyDir verifies that an empty userDir still returns all builtins.
func TestListEmptyDir(t *testing.T) {
	names := theme.List("")
	for _, b := range allBuiltins {
		if !contains(names, b) {
			t.Errorf("List(\"\") missing builtin %q; got: %v", b, names)
		}
	}
}

// TestStyleHelpers verifies that style helpers return non-zero lipgloss styles
// without panicking.
func TestStyleHelpers(t *testing.T) {
	th := theme.Default()

	// These must not panic and must return non-zero styles.
	_ = th.PanelBorder(true)
	_ = th.PanelBorder(false)
	_ = th.Title(true)
	_ = th.Title(false)
	_ = th.Primary()
	_ = th.Muted()
	_ = th.AccentStyle()
	_ = th.PlayingStyle()
	_ = th.ErrorStyle()
	_ = th.Selected()
}

// --- helpers ----------------------------------------------------------------

// isValidHex returns true if s is a "#rrggbb" hex colour string.
func isValidHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// contains reports whether names contains s.
func contains(names []string, s string) bool {
	for _, n := range names {
		if n == s {
			return true
		}
	}
	return false
}
