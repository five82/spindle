package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"spindle/internal/ipc"
)

func TestRenderStatusLineNoColor(t *testing.T) {
	got := renderStatusLine("Spindle", statusError, "Not running", false)
	want := fmt.Sprintf("%-*s %s", statusLabelWidth, "Spindle:", "[ERROR] Not running")
	if got != want {
		t.Fatalf("renderStatusLine mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderStatusLineWithColor(t *testing.T) {
	got := renderStatusLine("Spindle", statusOK, "Running", true)
	if !strings.HasPrefix(got, ansiGreen) {
		t.Fatalf("expected green prefix, got %q", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Fatalf("expected reset suffix, got %q", got)
	}
}

func TestDependencyLines(t *testing.T) {
	deps := []ipc.DependencyStatus{
		{Name: "MakeMKV", Available: false},
		{Name: "Drapto", Available: true, Command: "drapto"},
		{Name: "ntfy", Available: false, Optional: true, Detail: "not configured"},
	}
	lines := dependencyLines(deps, false)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "[ERROR] not available") {
		t.Fatalf("expected error detail in first line, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "[OK] Ready (command: drapto)") {
		t.Fatalf("expected ready detail in second line, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "[WARN] not configured") {
		t.Fatalf("expected warn detail in third line, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "Missing dependencies:") {
		t.Fatalf("expected missing dependencies summary, got %q", lines[3])
	}
}

func TestShouldColorizeNonFile(t *testing.T) {
	if shouldColorize(io.Discard) {
		t.Fatalf("expected non-file writer to disable color")
	}
}
