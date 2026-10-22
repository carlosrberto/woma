package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorktreeDir != Default().WorktreeDir || cfg.StaleDuration() != 30*24*time.Hour {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg, err := LoadFile(write(t, `
roots = ["~/code", "/srv/repos"]
ignore = ["vendor"]
max_depth = 4
stale_after = "2w"
editor = "code"
copy = []
install = "none"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roots[0] != filepath.Join(home, "code") || cfg.Roots[1] != "/srv/repos" {
		t.Errorf("roots = %v", cfg.Roots)
	}
	if cfg.MaxDepth != 4 || cfg.Editor != "code" || cfg.Install != "none" || len(cfg.Ignore) != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.Copy) != 0 {
		t.Errorf("explicit empty copy list should override default, got %v", cfg.Copy)
	}
	if cfg.WorktreeDir != Default().WorktreeDir {
		t.Errorf("unset worktree_dir should keep default, got %q", cfg.WorktreeDir)
	}
	if cfg.StaleDuration() != 14*24*time.Hour {
		t.Errorf("stale = %v", cfg.StaleDuration())
	}
}

func TestLoadErrors(t *testing.T) {
	for name, content := range map[string]string{
		"syntax":  "roots = [",
		"unknown": `colour = "red"`,
		"age":     `stale_after = "soon"`,
	} {
		if _, err := LoadFile(write(t, content)); err == nil || !strings.Contains(err.Error(), "config") {
			t.Errorf("%s: expected config error, got %v", name, err)
		}
	}
}

func TestParseAge(t *testing.T) {
	day := 24 * time.Hour
	for in, want := range map[string]time.Duration{
		"": 0, "30d": 30 * day, "2w": 14 * day, "6mo": 180 * day, "1y": 365 * day, "36h": 36 * time.Hour,
	} {
		if got, err := ParseAge(in); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"x", "-1d", "d", "1.5d"} {
		if _, err := ParseAge(bad); err == nil {
			t.Errorf("ParseAge(%q) should fail", bad)
		}
	}
}

func TestPathRespectsEnv(t *testing.T) {
	t.Setenv("WOMA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if got := Path(); got != "/tmp/xdg/woma/config.toml" {
		t.Errorf("Path() = %q", got)
	}
	t.Setenv("WOMA_CONFIG", "/etc/woma.toml")
	if got := Path(); got != "/etc/woma.toml" {
		t.Errorf("Path() = %q", got)
	}
}
