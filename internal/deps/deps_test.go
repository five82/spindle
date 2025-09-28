package deps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckBinaries(t *testing.T) {
	binDir := t.TempDir()
	present := filepath.Join(binDir, "present")
	script := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(present, script, 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	reqs := []Requirement{
		{Name: "Present", Command: present},
		{Name: "Missing", Command: "clearly-not-present-binary"},
	}

	results := CheckBinaries(reqs)
	if len(results) != len(reqs) {
		t.Fatalf("expected %d results, got %d", len(reqs), len(results))
	}

	if !results[0].Available {
		t.Fatalf("expected first requirement to be available, got %#v", results[0])
	}

	if results[1].Available {
		t.Fatalf("expected missing binary to be unavailable")
	}
	if results[1].Detail == "" {
		t.Fatalf("expected detail message for missing binary")
	}

	if results[1].Command != "clearly-not-present-binary" {
		t.Fatalf("unexpected command recorded: %s", results[1].Command)
	}

	if results[0].Detail != "" {
		t.Fatalf("unexpected detail for available dependency: %s", results[0].Detail)
	}
}
