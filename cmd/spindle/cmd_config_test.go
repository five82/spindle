package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestConfigInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	cmd := newConfigCmd()
	init, _, err := cmd.Find([]string{"init"})
	if err != nil {
		t.Fatal(err)
	}
	if init.Annotations["skipConfigLoad"] != "true" {
		t.Fatal("config init must work before config exists")
	}
	if err := init.Flags().Set("path", path); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := init.RunE(init, nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, path) {
		t.Fatalf("init output = %q, want destination path", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != config.SampleConfig() {
		t.Fatal("init did not write the sample config")
	}
	if err := init.RunE(init, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("init existing file = %v, want overwrite error", err)
	}
	if err := init.Flags().Set("overwrite", "true"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := init.RunE(init, nil); err != nil {
			t.Fatalf("overwrite: %v", err)
		}
	})
	data, err = os.ReadFile(path)
	if err != nil || string(data) != config.SampleConfig() {
		t.Fatalf("overwritten config = %q, %v", data, err)
	}
}

func TestConfigInitDefaultPathAndUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cmd := newConfigInitCmd()
	captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	path := filepath.Join(dir, "spindle", "config.toml")
	if data, err := os.ReadFile(path); err != nil || string(data) != config.SampleConfig() {
		t.Fatalf("default config: %q %v", data, err)
	}
	blocker := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd = newConfigInitCmd()
	if err := cmd.Flags().Set("path", blocker); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("directory accepted as config file")
	}
}

func TestConfigValidateEnsuresDirectories(t *testing.T) {
	t.Setenv("TMDB_API_KEY", "test-key")
	old := cfg
	t.Cleanup(func() { cfg = old })
	loaded, err := config.Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	loaded.Paths.StagingDir = filepath.Join(root, "staging")
	loaded.Paths.StateDir = filepath.Join(root, "state")
	loaded.Paths.ReviewDir = filepath.Join(root, "review")
	loaded.Paths.LibraryDir = filepath.Join(root, "library")
	cfg = loaded
	cmd := newConfigValidateCmd()
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Config: valid") {
		t.Fatalf("validation output: %s", got)
	}
	if _, err := os.Stat(cfg.Paths.StateDir); err != nil {
		t.Fatalf("state directory: %v", err)
	}
	blocker := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Paths.StateDir = filepath.Join(blocker, "state")
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "ensure directories") {
		t.Fatalf("blocked directory: %v", err)
	}
}

func TestConfigInitCannotCreateDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newConfigInitCmd()
	if err := cmd.Flags().Set("path", filepath.Join(parent, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "create config dir") {
		t.Fatalf("init with file parent = %v, want directory error", err)
	}
}
