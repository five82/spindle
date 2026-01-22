package ripping

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyMakeMKVSettingsCreatesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.conf")

	settings := map[string]string{
		"app_DefaultSelectionString": "-sel:test",
		"app_LibdriveIO":             "true",
	}
	if err := applyMakeMKVSettings(path, settings); err != nil {
		t.Fatalf("applyMakeMKVSettings returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "app_DefaultSelectionString = \"-sel:test\"") {
		t.Fatalf("expected selection rule in file, got: %s", content)
	}
	if !strings.Contains(content, "app_LibdriveIO = \"true\"") {
		t.Fatalf("expected libdrive setting in file, got: %s", content)
	}
}

func TestApplyMakeMKVSettingsPreservesExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.conf")

	initial := "# existing\nfoo = \"bar\"\napp_DefaultSelectionString = \"old\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	settings := map[string]string{
		"app_DefaultSelectionString": "-sel:new",
		"app_LibdriveIO":             "true",
	}
	if err := applyMakeMKVSettings(path, settings); err != nil {
		t.Fatalf("applyMakeMKVSettings returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	content := string(data)

	// Verify existing settings are preserved
	if !strings.Contains(content, "foo = \"bar\"") {
		t.Fatalf("expected existing setting foo preserved, got: %s", content)
	}
	// Verify new settings are applied
	if !strings.Contains(content, "app_DefaultSelectionString = \"-sel:new\"") {
		t.Fatalf("expected updated selection rule, got: %s", content)
	}
	if !strings.Contains(content, "app_LibdriveIO = \"true\"") {
		t.Fatalf("expected libdrive setting, got: %s", content)
	}
}

func TestApplyMakeMKVSettingsNoUpdateWhenCurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.conf")

	settings := map[string]string{
		"app_LibdriveIO": "true",
	}

	// First write
	if err := applyMakeMKVSettings(path, settings); err != nil {
		t.Fatalf("first applyMakeMKVSettings returned error: %v", err)
	}

	info1, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}

	// Second write with same settings should be a no-op
	if err := applyMakeMKVSettings(path, settings); err != nil {
		t.Fatalf("second applyMakeMKVSettings returned error: %v", err)
	}

	info2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file after second write: %v", err)
	}

	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("file was modified when settings were already current")
	}
}
