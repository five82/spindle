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

func TestCheckFFmpegForDraptoSidecar(t *testing.T) {
	tmp := t.TempDir()
	draptoName := executableName("drapto")
	ffmpegName := executableName("ffmpeg")
	draptoPath := filepath.Join(tmp, draptoName)
	ffmpegPath := filepath.Join(tmp, ffmpegName)
	script := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(draptoPath, script, 0o755); err != nil {
		t.Fatalf("write drapto stub: %v", err)
	}
	if err := os.WriteFile(ffmpegPath, script, 0o755); err != nil {
		t.Fatalf("write ffmpeg sidecar: %v", err)
	}

	status := CheckFFmpegForDrapto(draptoPath)
	if !status.Available {
		t.Fatalf("expected ffmpeg sidecar to be available, got detail %q", status.Detail)
	}
	if status.Command != ffmpegPath {
		t.Fatalf("expected ffmpeg command %q, got %q", ffmpegPath, status.Command)
	}
}

func TestCheckFFmpegForDraptoPathFallback(t *testing.T) {
	tmp := t.TempDir()
	draptoPath := filepath.Join(tmp, executableName("drapto"))
	script := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(draptoPath, script, 0o755); err != nil {
		t.Fatalf("write drapto stub: %v", err)
	}

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	ffmpegPath := filepath.Join(binDir, executableName("ffmpeg"))
	if err := os.WriteFile(ffmpegPath, script, 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}
	oldPath := os.Getenv("PATH")
	newPath := binDir
	if oldPath != "" {
		newPath = binDir + string(os.PathListSeparator) + oldPath
	}
	t.Setenv("PATH", newPath)

	status := CheckFFmpegForDrapto(draptoPath)
	if !status.Available {
		t.Fatalf("expected ffmpeg fallback to be available, got detail %q", status.Detail)
	}
	if status.Command != ffmpegPath {
		t.Fatalf("expected ffmpeg command %q, got %q", ffmpegPath, status.Command)
	}
}

func TestCheckFFmpegForDraptoNotFound(t *testing.T) {
	tmp := t.TempDir()
	draptoPath := filepath.Join(tmp, executableName("drapto"))
	t.Setenv("PATH", "")
	status := CheckFFmpegForDrapto(draptoPath)
	if status.Available {
		t.Fatal("expected ffmpeg resolution to fail")
	}
	if status.Detail == "" {
		t.Fatal("expected detail message when ffmpeg is unavailable")
	}
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
