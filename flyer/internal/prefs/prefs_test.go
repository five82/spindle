package prefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileUsesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := Load("")
	if p.Theme != defaultTheme {
		t.Fatalf("Theme = %q, want %q", p.Theme, defaultTheme)
	}
}

func TestLoad_ReadsExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	prefsDir := filepath.Join(home, ".config", "flyer")
	if err := os.MkdirAll(prefsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	prefsFile := filepath.Join(prefsDir, "prefs.toml")
	if err := os.WriteFile(prefsFile, []byte("theme = \"Slate\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := Load("")
	if p.Theme != "Slate" {
		t.Fatalf("Theme = %q, want %q", p.Theme, "Slate")
	}
}

func TestLoad_ExplicitPath(t *testing.T) {
	tmp := t.TempDir()
	prefsFile := filepath.Join(tmp, "custom.toml")
	if err := os.WriteFile(prefsFile, []byte("theme = \"Slate\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := Load(prefsFile)
	if p.Theme != "Slate" {
		t.Fatalf("Theme = %q, want %q", p.Theme, "Slate")
	}
}

func TestSave_CreatesFileAndDirs(t *testing.T) {
	tmp := t.TempDir()
	prefsFile := filepath.Join(tmp, "subdir", "prefs.toml")

	p := Prefs{Theme: "Slate"}
	if err := Save(prefsFile, p); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded := Load(prefsFile)
	if loaded.Theme != "Slate" {
		t.Fatalf("Theme = %q, want %q", loaded.Theme, "Slate")
	}
}

func TestLoad_EmptyThemeFallsBackToDefault(t *testing.T) {
	tmp := t.TempDir()
	prefsFile := filepath.Join(tmp, "prefs.toml")
	if err := os.WriteFile(prefsFile, []byte("theme = \"\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := Load(prefsFile)
	if p.Theme != defaultTheme {
		t.Fatalf("Theme = %q, want %q", p.Theme, defaultTheme)
	}
}

func TestLoad_InvalidTOMLFallsBackToDefault(t *testing.T) {
	tmp := t.TempDir()
	prefsFile := filepath.Join(tmp, "prefs.toml")
	if err := os.WriteFile(prefsFile, []byte("not valid toml {{{\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := Load(prefsFile)
	if p.Theme != defaultTheme {
		t.Fatalf("Theme = %q, want %q", p.Theme, defaultTheme)
	}
}
