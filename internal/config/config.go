// Package config handles tubeamp's configuration with XDG base-dir support.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Art palette modes for rendered cover art.
const (
	// ArtPaletteAuto renders covers with their own colors (median-cut to a
	// compact pixel-art palette).
	ArtPaletteAuto = "auto"
	// ArtPaletteTheme snaps cover colors to the active theme's palette.
	ArtPaletteTheme = "theme"
)

// Config holds tubeamp's user settings.
type Config struct {
	Theme      string `yaml:"theme"`       // catppuccin-mocha by default
	Volume     int    `yaml:"volume"`      // 0-100, default 80; 0 treated as unset
	MPVPath    string `yaml:"mpv_path"`    // default "mpv" (PATH lookup)
	YTDLFormat string `yaml:"ytdl_format"` // default "bestaudio"
	ArtPalette string `yaml:"art_palette"` // "auto" (default) or "theme"
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Theme:      "catppuccin-mocha",
		Volume:     80,
		MPVPath:    "mpv",
		YTDLFormat: "bestaudio",
		ArtPalette: ArtPaletteAuto,
	}
}

// Load reads the config from disk, returning Default() if the file is missing.
// Unset fields in the YAML are filled from defaults (treating Volume 0 as unset).
func Load() (*Config, error) {
	path := filepath.Join(ConfigDir(), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := Default() // Start with defaults
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Fill in defaults for unset fields
	if cfg.Theme == "" {
		cfg.Theme = "catppuccin-mocha"
	}
	if cfg.Volume == 0 {
		cfg.Volume = 80
	}
	if cfg.MPVPath == "" {
		cfg.MPVPath = "mpv"
	}
	if cfg.YTDLFormat == "" {
		cfg.YTDLFormat = "bestaudio"
	}
	if cfg.ArtPalette == "" {
		cfg.ArtPalette = ArtPaletteAuto
	}

	return cfg, nil
}

// Save writes the config to disk, creating directories as needed (0755, files 0644).
func (c *Config) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	path := filepath.Join(dir, "config.yaml")
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Write atomically: write to temp file, then rename
	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmpFile, path); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("moving config: %w", err)
	}

	return nil
}

// ConfigDir returns the config directory: $XDG_CONFIG_HOME/tubeamp or ~/.config/tubeamp.
func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "tubeamp")
	}
	return filepath.Join(homeDir(), ".config", "tubeamp")
}

// CacheDir returns the cache directory: $XDG_CACHE_HOME/tubeamp or ~/.cache/tubeamp.
func CacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tubeamp")
	}
	return filepath.Join(homeDir(), ".cache", "tubeamp")
}

// DataDir returns the data directory: $XDG_DATA_HOME/tubeamp or ~/.local/share/tubeamp.
func DataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "tubeamp")
	}
	return filepath.Join(homeDir(), ".local", "share", "tubeamp")
}

// ThemesDir returns the themes directory: ConfigDir()/themes.
func ThemesDir() string {
	return filepath.Join(ConfigDir(), "themes")
}

// homeDir returns the user's home directory.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback (should not happen in practice)
		return "~"
	}
	return home
}
