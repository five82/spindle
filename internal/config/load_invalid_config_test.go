package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsMissingMalformedAndInvalidExplicitConfigs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("missing explicit config: %v", err)
	}
	if err := os.WriteFile(path, []byte("[broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), "parse TOML") {
		t.Fatalf("malformed config: %v", err)
	}
	if err := os.WriteFile(path, []byte("[makemkv]\nrip_timeout = -1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), "rip_timeout") {
		t.Fatalf("invalid config: %v", err)
	}
}
