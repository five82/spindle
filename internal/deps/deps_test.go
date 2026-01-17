package deps

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestResolveFFmpegPathFromEnv(t *testing.T) {
	tmp := t.TempDir()
	ffmpegPath := filepath.Join(tmp, executableName("ffmpeg"))
	script := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(ffmpegPath, script, 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	t.Setenv("SPINDLE_FFMPEG_PATH", ffmpegPath)
	result := ResolveFFmpegPath()
	if result != ffmpegPath {
		t.Fatalf("expected ffmpeg path %q, got %q", ffmpegPath, result)
	}
}

func TestResolveFFmpegPathFromPATH(t *testing.T) {
	tmp := t.TempDir()
	ffmpegPath := filepath.Join(tmp, executableName("ffmpeg"))
	script := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(ffmpegPath, script, 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	// Clear env vars and set PATH
	t.Setenv("SPINDLE_FFMPEG_PATH", "")
	t.Setenv("FFMPEG_PATH", "")
	oldPath := os.Getenv("PATH")
	newPath := tmp
	if oldPath != "" {
		newPath = tmp + string(os.PathListSeparator) + oldPath
	}
	t.Setenv("PATH", newPath)

	result := ResolveFFmpegPath()
	if result != ffmpegPath {
		t.Fatalf("expected ffmpeg path %q, got %q", ffmpegPath, result)
	}
}

func TestResolveFFmpegPathFallback(t *testing.T) {
	t.Setenv("SPINDLE_FFMPEG_PATH", "")
	t.Setenv("FFMPEG_PATH", "")
	t.Setenv("PATH", "")

	result := ResolveFFmpegPath()
	if result != "ffmpeg" {
		t.Fatalf("expected fallback to 'ffmpeg', got %q", result)
	}
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
