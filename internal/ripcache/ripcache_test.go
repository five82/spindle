package ripcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/queue"
)

func TestStoreAndRestore(t *testing.T) {
	base := t.TempDir()
	cfg := config.Default()
	cfg.RipCacheEnabled = true
	cfg.RipCacheDir = base
	cfg.RipCacheMaxGiB = 1

	manager := NewManager(&cfg, slog.Default())
	if manager == nil {
		t.Fatalf("expected manager")
	}

	// Build a fake rip directory.
	ripDir := filepath.Join(t.TempDir(), "rips")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		t.Fatalf("mk rip dir: %v", err)
	}
	content := []byte("hello world")
	if err := os.WriteFile(filepath.Join(ripDir, "movie.mkv"), content, 0o644); err != nil {
		t.Fatalf("write rip file: %v", err)
	}

	item := &queue.Item{ID: 42, DiscFingerprint: "abcd1234", DiscTitle: "Demo"}
	if err := manager.Store(context.Background(), item, ripDir); err != nil {
		t.Fatalf("store: %v", err)
	}

	restoreDir := filepath.Join(t.TempDir(), "restored")
	restored, err := manager.Restore(context.Background(), item, restoreDir)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored {
		t.Fatalf("expected restore to occur")
	}
	data, err := os.ReadFile(filepath.Join(restoreDir, "movie.mkv"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("unexpected restored content: %q", data)
	}
}

func TestPruneBySize(t *testing.T) {
	base := t.TempDir()
	cfg := config.Default()
	cfg.RipCacheEnabled = true
	cfg.RipCacheDir = base
	cfg.RipCacheMaxGiB = 1 // small budget

	manager := NewManager(&cfg, slog.Default())

	// Override statfs to ignore free-space logic in this test.
	manager.statfs = func(string) (uint64, uint64, error) {
		return 100, 50, nil
	}

	itemOld := &queue.Item{ID: 1, DiscFingerprint: "old"}
	itemNew := &queue.Item{ID: 2, DiscFingerprint: "new"}

	makeRip := func(sizeKB int) string {
		dir := t.TempDir()
		ripDir := filepath.Join(dir, "rips")
		if err := os.MkdirAll(ripDir, 0o755); err != nil {
			t.Fatalf("mk rip dir: %v", err)
		}
		data := make([]byte, sizeKB*1024)
		if err := os.WriteFile(filepath.Join(ripDir, "file.mkv"), data, 0o644); err != nil {
			t.Fatalf("write rip file: %v", err)
		}
		return ripDir
	}

	if err := manager.Store(context.Background(), itemOld, makeRip(800*1024)); err != nil { // ~0.78 GiB
		t.Fatalf("store old: %v", err)
	}
	// Ensure old entry has an earlier mod time.
	oldPath := manager.cachePath(itemOld)
	if err := os.Chtimes(oldPath, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := manager.Store(context.Background(), itemNew, makeRip(400*1024)); err != nil {
		t.Fatalf("store new: %v", err)
	}

	// Budget 1 GiB => oldest should be pruned.
	if existsNonEmptyDir(oldPath) {
		t.Fatalf("expected oldest cache entry to be pruned")
	}
	if !existsNonEmptyDir(manager.cachePath(itemNew)) {
		t.Fatalf("expected newest cache entry to remain")
	}
}
