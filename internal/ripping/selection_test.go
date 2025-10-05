package ripping

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyMakeMKVSelectionRuleCreatesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.conf")

	rule := "-sel:test"
	if err := applyMakeMKVSelectionRule(path, rule); err != nil {
		t.Fatalf("applyMakeMKVSelectionRule returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "app_DefaultSelectionString = \"-sel:test\"") {
		t.Fatalf("expected selection rule in file, got: %s", content)
	}
}

func TestApplyMakeMKVSelectionRulePreservesSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.conf")

	initial := "# existing\nfoo = \"bar\"\napp_DefaultSelectionString = \"old\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	rule := "-sel:new"
	if err := applyMakeMKVSelectionRule(path, rule); err != nil {
		t.Fatalf("applyMakeMKVSelectionRule returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	expected := []string{
		"# MakeMKV settings file (managed by Spindle)",
		"foo = \"bar\"",
		"app_DefaultSelectionString = \"-sel:new\"",
	}

	if len(lines) != len(expected) {
		t.Fatalf("unexpected line count: got %d, want %d (%v)", len(lines), len(expected), lines)
	}

	for i, line := range lines {
		if line != expected[i] {
			t.Fatalf("line %d mismatch: got %q, want %q", i, line, expected[i])
		}
	}
}
