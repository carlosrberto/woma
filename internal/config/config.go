// Package config loads woma's optional TOML configuration file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds user settings. Every field is optional.
type Config struct {
	// Roots are scanned when no path is given on the command line.
	Roots []string `toml:"roots"`
	// Ignore lists directory names (or globs) skipped while scanning, in
	// addition to node_modules.
	Ignore []string `toml:"ignore"`
	// MaxDepth limits how deep scanning goes below a root; 0 means no limit.
	MaxDepth int `toml:"max_depth"`
	// StaleAfter is how long without activity before a worktree is "stale",
	// e.g. "30d", "2w" or "36h".
	StaleAfter string `toml:"stale_after"`
	// Editor opens a worktree; defaults to $VISUAL, $EDITOR, then the OS opener.
	Editor string `toml:"editor"`
	// WorktreeDir is where `woma new` creates worktrees. {repo} is the main
	// worktree path, {repo_name} its folder name and {name} the branch with
	// "/" replaced by "-".
	WorktreeDir string `toml:"worktree_dir"`
	// Copy lists file name patterns copied from the main worktree into new
	// worktrees (when missing there). They are also protected from
	// artifact cleaning.
	Copy []string `toml:"copy"`
	// Install is run in new worktrees; empty means auto-detect from lockfiles
	// and "none" disables it.
	Install string `toml:"install"`
}

// Default returns the built-in settings.
func Default() Config {
	return Config{
		StaleAfter:  "30d",
		WorktreeDir: "{repo}/.worktrees/{name}",
		Copy:        []string{".env", ".env.*"},
	}
}

// Path returns the config file location: $XDG_CONFIG_HOME/woma/config.toml,
// falling back to ~/.config/woma/config.toml.
func Path() string {
	if p := os.Getenv("WOMA_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "woma", "config.toml")
}

// Load reads the config file, applying defaults for anything unset. A
// missing file is not an error.
func Load() (Config, error) {
	return LoadFile(Path())
}

// LoadFile reads the config at path, applying defaults for anything unset.
func LoadFile(path string) (Config, error) {
	cfg := Default()
	var file Config
	md, err := toml.DecodeFile(path, &file)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return cfg, fmt.Errorf("config %s: unknown setting %q", path, undecoded[0].String())
	}

	if md.IsDefined("roots") {
		cfg.Roots = file.Roots
	}
	if md.IsDefined("ignore") {
		cfg.Ignore = file.Ignore
	}
	if md.IsDefined("max_depth") {
		cfg.MaxDepth = file.MaxDepth
	}
	if md.IsDefined("stale_after") {
		cfg.StaleAfter = file.StaleAfter
	}
	if md.IsDefined("editor") {
		cfg.Editor = file.Editor
	}
	if md.IsDefined("worktree_dir") {
		cfg.WorktreeDir = file.WorktreeDir
	}
	if md.IsDefined("copy") {
		cfg.Copy = file.Copy
	}
	if md.IsDefined("install") {
		cfg.Install = file.Install
	}
	for i, r := range cfg.Roots {
		cfg.Roots[i] = ExpandHome(r)
	}
	if _, err := ParseAge(cfg.StaleAfter); err != nil {
		return cfg, fmt.Errorf("config %s: stale_after: %w", path, err)
	}
	return cfg, nil
}

// StaleDuration returns StaleAfter parsed; it is validated by Load.
func (c Config) StaleDuration() time.Duration {
	d, _ := ParseAge(c.StaleAfter)
	return d
}

// ParseAge parses durations like "30d", "2w", "6mo", "1y" or any Go duration
// ("36h"). An empty string means zero.
func ParseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	units := []struct {
		suffix string
		d      time.Duration
	}{
		{"mo", 30 * 24 * time.Hour},
		{"d", 24 * time.Hour},
		{"w", 7 * 24 * time.Hour},
		{"y", 365 * 24 * time.Hour},
	}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			n, err := strconv.Atoi(num)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("invalid age %q", s)
			}
			return time.Duration(n) * u.d, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid age %q (use e.g. 30d, 2w, 6mo, 36h)", s)
	}
	return d, nil
}

// ExpandHome replaces a leading "~" with the home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
