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
