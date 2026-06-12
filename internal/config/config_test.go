package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Theme != "catppuccin-mocha" {
		t.Errorf("Default theme: got %q, want %q", cfg.Theme, "catppuccin-mocha")
	}
	if cfg.Volume != 80 {
		t.Errorf("Default volume: got %d, want 80", cfg.Volume)
	}
	if cfg.MPVPath != "mpv" {
		t.Errorf("Default MPVPath: got %q, want %q", cfg.MPVPath, "mpv")
	}
	if cfg.YTDLFormat != "bestaudio" {
		t.Errorf("Default YTDLFormat: got %q, want %q", cfg.YTDLFormat, "bestaudio")
	}
}

func TestLoadMissingFile(t *testing.T) {
	// Set XDG vars to temp directory (file doesn't exist)
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with missing file: got error %v, want nil", err)
	}
	if cfg == nil {
		t.Fatal("Load() with missing file: got nil config, want Default()")
	}
	// Should return defaults
	if cfg.Theme != "catppuccin-mocha" || cfg.Volume != 80 {
		t.Errorf("Load() missing file: got %+v, want defaults", cfg)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Save a custom config
	cfg := &Config{
		Theme:      "nord",
		Volume:     50,
		MPVPath:    "/usr/bin/mpv",
		YTDLFormat: "best",
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// Check that file exists with correct permissions
	configPath := filepath.Join(tmpDir, "tubeamp", "config.yaml")
	fi, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("config.yaml stat: %v", err)
	}
	if fi.Mode()&0644 != 0644 {
		t.Errorf("config.yaml perms: got %o, want 0644", fi.Mode().Perm())
	}

	// Load it back
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if loaded.Theme != cfg.Theme || loaded.Volume != cfg.Volume ||
		loaded.MPVPath != cfg.MPVPath || loaded.YTDLFormat != cfg.YTDLFormat {
		t.Errorf("Roundtrip: got %+v, want %+v", loaded, cfg)
	}
}

func TestLoadPartialYAML(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Write partial YAML manually
	configDir := filepath.Join(tmpDir, "tubeamp")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("theme: gruvbox-dark\nmpv_path: /custom/mpv\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load should fill in missing fields from defaults
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.Theme != "gruvbox-dark" {
		t.Errorf("Theme: got %q, want gruvbox-dark", cfg.Theme)
	}
	if cfg.MPVPath != "/custom/mpv" {
		t.Errorf("MPVPath: got %q, want /custom/mpv", cfg.MPVPath)
	}
	// Unset fields should have defaults
	if cfg.Volume != 80 {
		t.Errorf("Volume (unset): got %d, want 80", cfg.Volume)
	}
	if cfg.YTDLFormat != "bestaudio" {
		t.Errorf("YTDLFormat (unset): got %q, want bestaudio", cfg.YTDLFormat)
	}
	if cfg.ArtPalette != ArtPaletteAuto {
		t.Errorf("ArtPalette (unset): got %q, want %q", cfg.ArtPalette, ArtPaletteAuto)
	}
}

func TestLoadZeroVolume(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Write YAML with volume: 0
	configDir := filepath.Join(tmpDir, "tubeamp")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("volume: 0\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load should treat 0 as unset and use default
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.Volume != 80 {
		t.Errorf("Volume: got %d (treated as unset), want 80", cfg.Volume)
	}
}

func TestDirs(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		dirFunc func() string
		expect  string
	}{
		{
			name:    "ConfigDir with XDG_CONFIG_HOME",
			envKey:  "XDG_CONFIG_HOME",
			envVal:  "/custom",
			dirFunc: ConfigDir,
			expect:  "/custom/tubeamp",
		},
		{
			name:    "CacheDir with XDG_CACHE_HOME",
			envKey:  "XDG_CACHE_HOME",
			envVal:  "/tmp/cache",
			dirFunc: CacheDir,
			expect:  "/tmp/cache/tubeamp",
		},
		{
			name:    "DataDir with XDG_DATA_HOME",
			envKey:  "XDG_DATA_HOME",
			envVal:  "/tmp/data",
			dirFunc: DataDir,
			expect:  "/tmp/data/tubeamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.envVal)
			got := tt.dirFunc()
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestThemesDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	expected := filepath.Join(tmpDir, "tubeamp", "themes")
	got := ThemesDir()
	if got != expected {
		t.Errorf("ThemesDir: got %q, want %q", got, expected)
	}
}

func TestDirsWithoutXDG(t *testing.T) {
	// Clear XDG vars
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	configDir := ConfigDir()
	if !strings.HasSuffix(configDir, ".config/tubeamp") {
		t.Errorf("ConfigDir (no XDG): got %q, want to end with .config/tubeamp", configDir)
	}

	cacheDir := CacheDir()
	if !strings.HasSuffix(cacheDir, ".cache/tubeamp") {
		t.Errorf("CacheDir (no XDG): got %q, want to end with .cache/tubeamp", cacheDir)
	}

	dataDir := DataDir()
	if !strings.HasSuffix(dataDir, ".local/share/tubeamp") {
		t.Errorf("DataDir (no XDG): got %q, want to end with .local/share/tubeamp", dataDir)
	}
}
