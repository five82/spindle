package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestCacheProcessEnqueuesThroughDaemon(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}, RipCache: config.RipCacheConfig{MaxGiB: 1}}
	flagSocket = filepath.Join(dir, "api.sock")
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "movie", ID: 55, Title: "Test Film"}}
	data, err := env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	cache := ripcache.New(cfg.RipCacheDir(), 1)
	entry := ripcache.EntryMetadata{Fingerprint: "abcdef1234567890", DiscTitle: "Disc One", RipSpecData: data, CachedAt: time.Now()}
	if err := cache.WriteMetadata(entry.Fingerprint, entry); err != nil {
		t.Fatal(err)
	}
	process := newCacheProcessCmd()
	got := captureStdout(t, func() {
		if err := process.RunE(process, []string{"abc"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Queued: Disc One") {
		t.Fatalf("process output: %s", got)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].RipSpecData != data {
		t.Fatalf("queued item: %+v %v", items, err)
	}
	if err := process.RunE(process, []string{"abc"}); err == nil {
		t.Fatal("duplicate cache entry was queued")
	}
	// Mutations must fail when the daemon is absent, even if the DB exists.
	flagSocket = filepath.Join(dir, "missing.sock")
	if err := process.RunE(process, []string{"abc"}); err == nil {
		t.Fatal("processed cache entry without daemon")
	}
	if _, err := os.Stat(cfg.QueueDBPath()); err != nil {
		t.Fatal(err)
	}
}
