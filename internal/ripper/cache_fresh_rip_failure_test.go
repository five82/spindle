package ripper

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestCacheFreshRipPrunesBeforeCopy(t *testing.T) {
	sess := newRipSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	cacheDir := filepath.Join(root, "spindle", "rips")
	cache := ripcache.New(cacheDir, 1)
	if err := cache.WriteMetadata("old", ripcache.EntryMetadata{Fingerprint: "old", CachedAt: time.Now().Add(-time.Hour), TotalBytes: 1<<30 - 1<<20}); err != nil {
		t.Fatal(err)
	}
	ripped := filepath.Join(root, "ripped")
	if err := os.Mkdir(ripped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ripped, "film.mkv"), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: root}, RipCache: config.RipCacheConfig{MaxGiB: 1}}, nil, cache, nil, NoTitleOverride)
	h.cacheFree = func(string) int64 { return 200 << 30 } // runner /tmp need not have 100 GiB
	h.cacheFreshRip(slog.Default(), sess, ripped, 1)
	if cache.HasCache("old") || !cache.HasCache(sess.Item.DiscFingerprint) {
		t.Fatal("old cache entry should be pruned before the fresh rip is cached")
	}
}

func TestCacheFreshRipSkipsWhenCopyConsumesCushion(t *testing.T) {
	sess := newRipSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	cacheDir := filepath.Join(root, "spindle", "rips")
	cache := ripcache.New(cacheDir, 1)
	if err := cache.WriteMetadata("keep", ripcache.EntryMetadata{Fingerprint: "keep", CachedAt: time.Now(), TotalBytes: 1}); err != nil {
		t.Fatal(err)
	}
	ripped := filepath.Join(root, "ripped")
	if err := os.Mkdir(ripped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ripped, "film.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: root}, RipCache: config.RipCacheConfig{MaxGiB: 1}}, nil, cache, nil, NoTitleOverride)
	h.cacheFree = func(string) int64 { return 100 << 30 }
	var out bytes.Buffer
	h.cacheFreshRip(slog.New(slog.NewTextHandler(&out, nil)), sess, ripped, 1)
	if cache.HasCache(sess.Item.DiscFingerprint) || !cache.HasCache("keep") || !strings.Contains(out.String(), "working-space cushion") {
		t.Fatalf("cache copy should be skipped without evicting existing entries: %s", out.String())
	}
}

func TestCacheFreshRipSkipsOversizedCopyWithoutEviction(t *testing.T) {
	sess := newRipSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	cacheDir := filepath.Join(root, "spindle", "rips")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := ripcache.New(cacheDir, 1)
	ripped := filepath.Join(root, "ripped")
	if err := os.Mkdir(ripped, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(ripped, "large.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(1<<30 + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: root}, RipCache: config.RipCacheConfig{MaxGiB: 1}}, nil, cache, nil, NoTitleOverride)
	var out bytes.Buffer
	h.cacheFreshRip(slog.New(slog.NewTextHandler(&out, nil)), sess, ripped, 1)
	if cache.HasCache(sess.Item.DiscFingerprint) || !strings.Contains(out.String(), "rip exceeds cache cap") {
		t.Fatalf("oversized rip should not be copied: %s", out.String())
	}
}

func TestCacheFreshRipFailureLeavesNoUnusableEntry(t *testing.T) {
	sess := newRipSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	dir := t.TempDir()
	ripped := filepath.Join(dir, "ripped")
	if err := os.Mkdir(ripped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ripped, "film.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "cache")
	cache := ripcache.New(root, 1)
	h := New(&config.Config{}, nil, cache, nil, NoTitleOverride)
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	// Register succeeds, but a pre-existing directory at the metadata path
	// prevents the atomic rename. The incomplete entry must be removed.
	metaDir := filepath.Join(root, sess.Item.DiscFingerprint, "spindle.cache.json")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h.cacheFreshRip(logger, sess, ripped, 1)
	if cache.HasCache(sess.Item.DiscFingerprint) {
		t.Fatal("incomplete entry remained after metadata failure")
	}
	if !strings.Contains(out.String(), "rip cache metadata write failed") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.cacheFreshRip(logger, sess, ripped, 1)
	if !strings.Contains(out.String(), "rip cache write failed") {
		t.Fatal(out.String())
	}
}
