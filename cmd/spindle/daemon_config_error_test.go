package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonCommandRejectsInvalidConfig(t *testing.T) {
	old := flagConfig
	t.Cleanup(func() { flagConfig = old })
	flagConfig = filepath.Join(t.TempDir(), "nonexistent.toml")
	cmd := newDaemonCmd()
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "load config") {
		t.Fatalf("daemon: %v", err)
	}
}

func TestStatusCheckPathReportsMissingAndPresentDirectories(t *testing.T) {
	dir := t.TempDir()
	got := captureStdout(t, func() {
		checkPath("Missing", filepath.Join(dir, "missing"))
		checkPath("Present", dir)
		checkPath("Empty", "")
	})
	for _, want := range []string{"Missing", "Present", "(not configured)"} {
		if !strings.Contains(got, want) {
			t.Errorf("path output missing %q: %s", want, got)
		}
	}
}
