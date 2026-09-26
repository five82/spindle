package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsConfigurationError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("[api\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), Options{ConfigPath: path, PrefsPath: filepath.Join(home, "prefs.toml")})
	if err == nil || !strings.Contains(err.Error(), "load spindle config") {
		t.Fatalf("Run error = %v, want configuration error", err)
	}
}

func TestRunReportsInvalidEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	err := Run(context.Background(), Options{
		ConfigPath:  filepath.Join(home, "missing.toml"),
		PrefsPath:   filepath.Join(home, "prefs.toml"),
		APIEndpoint: "not a URL",
		APIToken:    "test-token",
	})
	if err == nil || !strings.Contains(err.Error(), "init spindle client") {
		t.Fatalf("Run error = %v, want client initialization error", err)
	}
}
