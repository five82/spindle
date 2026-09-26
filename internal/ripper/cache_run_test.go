package ripper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestRunRestoresValidCachedMovieWithoutDrive(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"codec_type\":\"video\"},{\"codec_type\":\"audio\"}],\"format\":{\"duration\":\"120\"}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	staging := t.TempDir()
	sess := ripCoverageSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	src := t.TempDir()
	file, err := os.Create(filepath.Join(src, "movie_t01.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(minRipFileSizeBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cache := ripcache.New(t.TempDir(), 1)
	if err := cache.Register(sess.Item.DiscFingerprint, src, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.WriteMetadata(sess.Item.DiscFingerprint, ripcache.EntryMetadata{TitleCount: 1, TotalBytes: minRipFileSizeBytes + 1}); err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: staging}}, nil, cache, nil, NoTitleOverride)
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatalf("cached rip: %v", err)
	}
	fresh, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := saved.Assets.FindAsset(ripspec.AssetKindRipped, "main")
	if !ok || asset.TitleID != 1 || !asset.IsCompleted() {
		t.Fatalf("restored asset: %+v %t", asset, ok)
	}
}

func TestCacheFreshRipStoresMetadataAndFiles(t *testing.T) {
	sess := ripCoverageSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie", DiscNumber: 3}})
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "movie_t01.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := ripcache.New(t.TempDir(), 1)
	h := New(&config.Config{}, nil, cache, nil, NoTitleOverride)
	h.cacheFreshRip(testLogger(), sess, src, 1)
	meta, err := cache.GetMetadata(sess.Item.DiscFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if meta.TotalBytes != 5 || meta.TitleCount != 1 || meta.DiscNumber != 3 || meta.RipSpecData == "" {
		t.Fatalf("cache metadata: %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(src, "movie_t01.mkv")); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	if restored, err := cache.Restore(sess.Item.DiscFingerprint, t.TempDir(), nil); err != nil || restored == nil {
		t.Fatalf("restore: %+v %v", restored, err)
	}
}
