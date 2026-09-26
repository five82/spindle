package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripcache"
)

func TestResolveTargetFromCacheAndFile(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}, RipCache: config.RipCacheConfig{MaxGiB: 1}}
	if _, err := resolveTarget("1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("absent entry: %v", err)
	}
	if _, err := resolveTarget(filepath.Join(dir, "missing.mkv")); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("absent file: %v", err)
	}
	store := ripcache.New(cfg.RipCacheDir(), 1)
	const fp = "123456abcdef"
	if err := store.WriteMetadata(fp, ripcache.EntryMetadata{Fingerprint: fp, DiscTitle: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTarget("1"); err == nil || !strings.Contains(err.Error(), "no video files") {
		t.Fatalf("metadata-only entry: %v", err)
	}
	video := filepath.Join(cfg.RipCacheDir(), fp, "title.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"1", video} {
		got, err := resolveTarget(input)
		if err != nil || got != video {
			t.Fatalf("resolve %q = %q, %v", input, got, err)
		}
	}
}
