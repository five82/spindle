//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMountPointLsblkAndFallback(t *testing.T) {
	root := t.TempDir()
	mp, cleanup, err := ResolveMountPoint(context.Background(), "/dev/nonexistent", root, nil)
	if err != nil || mp != root {
		t.Fatalf("lsblk mount: %q %v", mp, err)
	}
	cleanup()
	original := fallbackMountPaths
	fallbackMountPaths = []string{root}
	t.Cleanup(func() { fallbackMountPaths = original })
	if err := os.Mkdir(filepath.Join(root, "BDMV"), 0o755); err != nil {
		t.Fatal(err)
	}
	mp, cleanup, err = ResolveMountPoint(context.Background(), "/dev/nonexistent", "", nil)
	if err != nil || mp != root {
		t.Fatalf("fallback mount: %q %v", mp, err)
	}
	cleanup()
}

func TestResolveMountPointFailedAutoMount(t *testing.T) {
	original := fallbackMountPaths
	fallbackMountPaths = nil
	t.Cleanup(func() { fallbackMountPaths = original })
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mount"), []byte("#!/bin/sh\necho no fstab entry >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mp, cleanup, err := ResolveMountPoint(context.Background(), "/dev/not-a-disc", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no fstab entry") || mp != "" {
		t.Fatalf("auto mount: %q %v", mp, err)
	}
	cleanup()
}

func TestFindInProcMountsUnknownDevice(t *testing.T) {
	mp, err := findInProcMounts("/dev/this-path-does-not-exist-spindle-test")
	if err != nil || mp != "" {
		t.Fatalf("mount: %q %v", mp, err)
	}
}

func TestDetectAsyncPausedAndNoDisc(t *testing.T) {
	m := New("/dev/sr0", nil, nil, nil)
	m.PauseDisc()
	result, err := m.DetectAsync(context.Background())
	if err != nil || result == nil || result.Handled || !strings.Contains(result.Message, "paused") {
		t.Fatalf("paused: %+v %v", result, err)
	}
	m.ResumeDisc()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lsblk"), []byte("#!/bin/sh\necho '{\"blockdevices\":[]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err = m.DetectAsync(context.Background())
	// Empty lsblk is a parse error, and must release the processing guard.
	if err == nil || result != nil || m.processing {
		t.Fatalf("empty lsblk: %+v %v processing=%v", result, err, m.processing)
	}
}

func TestWaitForReadyCancellationAndMissingDrive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForReady(ctx, "/dev/nonexistent", nil); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	if err := WaitForReady(context.Background(), "/dev/nonexistent", nil); err == nil || !strings.Contains(err.Error(), "drive status poll 1") {
		t.Fatalf("missing drive: %v", err)
	}
	if _, err := DriveStatus("/dev/nonexistent"); err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("open: %v", err)
	}
}
