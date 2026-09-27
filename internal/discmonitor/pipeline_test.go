//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestEnqueuePipelineNewAndDuplicate(t *testing.T) {
	mount := t.TempDir()
	if err := os.Mkdir(filepath.Join(mount, "VIDEO_TS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "VIDEO_TS", "VIDEO_TS.IFO"), []byte("disc contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := New("/dev/not-a-drive", store, nil, nil)
	event := &DiscEvent{Device: "/dev/not-a-drive", Label: "01_MY_MOVIE", DiscType: "Unknown", MountPath: mount}

	first, err := m.enqueuePipeline(context.Background(), event)
	if err != nil || first == nil || first.Duplicate || first.Item.DiscTitle != "MY MOVIE" || event.DiscType != "DVD" {
		t.Fatalf("first enqueue = %+v, event = %+v, err = %v", first, event, err)
	}
	second, err := m.enqueuePipeline(context.Background(), event)
	if err != nil || second == nil || !second.Duplicate || second.Item.ID != first.Item.ID {
		t.Fatalf("duplicate enqueue = %+v, err = %v", second, err)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("queue items = %+v, err = %v", items, err)
	}
	// A second disc with no label must remain queueable under its fallback title.
	if err := os.WriteFile(filepath.Join(mount, "VIDEO_TS", "VIDEO_TS.IFO"), []byte("different disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	unnamed, err := m.enqueuePipeline(context.Background(), &DiscEvent{Device: "/dev/not-a-drive", DiscType: "DVD", MountPath: mount})
	if err != nil || unnamed == nil || unnamed.Item.DiscTitle != "Unknown Disc" {
		t.Fatalf("unnamed disc: %+v, %v", unnamed, err)
	}
}

func TestDetectionReportsClosedStoreAndMountFailures(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	mount := t.TempDir()
	if err := os.WriteFile(filepath.Join(mount, "disc.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New("/dev/not-a-drive", store, nil, nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Detect(context.Background()); err == nil || !strings.Contains(err.Error(), "check disc dependent items") {
		t.Fatalf("Detect: %v", err)
	}
	if _, err := m.DetectAsync(context.Background()); err == nil || !strings.Contains(err.Error(), "check disc dependent items") {
		t.Fatalf("DetectAsync: %v", err)
	}
	if _, err := m.enqueuePipeline(context.Background(), &DiscEvent{Device: "/dev/not-a-drive", MountPath: mount}); err == nil || !strings.Contains(err.Error(), "check duplicate fingerprint") {
		t.Fatalf("closed store: %v", err)
	}
	if _, err := m.enqueuePipeline(context.Background(), &DiscEvent{Device: "/dev/not-a-drive", MountPath: ""}); err == nil || !strings.Contains(err.Error(), "resolve mount point") {
		t.Fatalf("missing mount: %v", err)
	}
}

func TestEnqueuePipelineMissingStore(t *testing.T) {
	mount := t.TempDir()
	m := New("", nil, nil, nil)
	_, err := m.enqueuePipeline(context.Background(), &DiscEvent{MountPath: mount})
	if err == nil || !strings.Contains(err.Error(), "queue store not configured") {
		t.Fatalf("missing store error = %v", err)
	}
}

func TestProbeDiscAndDetection(t *testing.T) {
	bin := t.TempDir()
	lsblk := filepath.Join(bin, "lsblk")
	// A fake lsblk exercises the real command boundary without probing a physical drive.
	if err := os.WriteFile(lsblk, []byte("#!/bin/sh\nprintf '%s\\n' '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"  MY_DISC  \",\"fstype\":\"udf\",\"mountpoint\":\"/tmp/disc\"}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := New("/dev/sr0", nil, nil, nil)
	event, err := m.Detect(context.Background())
	if err != nil || event == nil || event.Label != "MY_DISC" || event.DiscType != "Blu-ray" || event.MountPath != "/tmp/disc" {
		t.Fatalf("Detect = %+v, %v", event, err)
	}
	if m.processing {
		t.Fatal("Detect left monitor processing flag set")
	}
}

func TestProbeDiscBadOutputReleasesGuard(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lsblk"), []byte("#!/bin/sh\necho invalid-json\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := New("/dev/sr0", nil, nil, nil)
	if event, err := m.Detect(context.Background()); err == nil || event != nil {
		t.Fatalf("Detect bad lsblk = %+v, %v", event, err)
	}
	if m.processing {
		t.Fatal("Detect left monitor processing flag set")
	}
	if result, err := m.DetectAsync(context.Background()); err == nil || result != nil {
		t.Fatalf("DetectAsync bad lsblk = %+v, %v", result, err)
	}
	if m.processing {
		t.Fatal("DetectAsync left monitor processing flag set")
	}
}
