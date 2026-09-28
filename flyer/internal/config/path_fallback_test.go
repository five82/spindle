package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathFallbacksAndBadConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative-config")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative XDG config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, err := resolvePath(""); err != nil || got != filepath.Join(home, ".config", "spindle", "config.toml") {
		t.Fatalf("default config = %q, %v", got, err)
	}
	if got := (Config{}).DaemonLogPath(); got != filepath.Join(home, ".local", "state", "spindle", "daemon.log") {
		t.Fatalf("default daemon log = %q", got)
	}
	if got := mustExpand(" "); got != " " {
		t.Fatalf("failed expansion = %q", got)
	}
	path := filepath.Join(home, "directory.toml")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("directory config: %v", err)
	}
	if _, err := Load(filepath.Join(path, "missing", "config.toml")); err != nil {
		t.Fatalf("missing config: %v", err)
	}
}
