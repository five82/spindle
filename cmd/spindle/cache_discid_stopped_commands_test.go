package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueaccess"
	"github.com/five82/spindle/internal/ripcache"
)

func TestCacheCommandsAgainstTemporaryStore(t *testing.T) {
	oldCfg, oldSocket, oldVerbose := cfg, flagSocket, flagVerbose
	t.Cleanup(func() { cfg, flagSocket, flagVerbose = oldCfg, oldSocket, oldVerbose })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}, RipCache: config.RipCacheConfig{MaxGiB: 1}}
	flagSocket = filepath.Join(dir, "absent.sock")
	flagVerbose = false
	store := ripcache.New(cfg.RipCacheDir(), 1)
	list := newCacheListCmd()
	if got := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(got, "No cached entries") {
		t.Fatal(got)
	}
	for i, e := range []ripcache.EntryMetadata{
		{Fingerprint: "aaa111222333444", DiscTitle: "First", TitleCount: 1, TotalBytes: 123, CachedAt: time.Now().Add(-time.Hour)},
		{Fingerprint: "bbb111222333444", DiscTitle: "Second", DiscNumber: 2, TitleCount: 2, TotalBytes: 456, CachedAt: time.Now()},
	} {
		if err := store.WriteMetadata(e.Fingerprint, e); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
	if e, err := cacheEntryByNumber(1); err != nil || e.DiscTitle != "Second" {
		t.Fatalf("first entry: %+v %v", e, err)
	}
	if _, err := cacheEntryByNumber(3); err == nil {
		t.Fatal("missing entry accepted")
	}
	if e, err := cacheEntryBySelector("aaa"); err != nil || e.DiscTitle != "First" {
		t.Fatalf("prefix: %+v %v", e, err)
	}
	for _, verbose := range []bool{false, true} {
		flagVerbose = verbose
		got := captureStdout(t, func() {
			if err := list.RunE(list, nil); err != nil {
				t.Fatal(err)
			}
		})
		for _, want := range []string{"First", "Second", "579"} {
			if !strings.Contains(got, want) {
				t.Errorf("list verbose=%v missing %q: %s", verbose, want, got)
			}
		}
	}
	flagVerbose = false
	if err := list.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	var entries []ripcache.EntryMetadata
	if err := json.Unmarshal([]byte(got), &entries); err != nil || len(entries) != 2 {
		t.Fatalf("list json: %s %v", got, err)
	}
	process := newCacheProcessCmd()
	if err := process.RunE(process, []string{"aaa"}); err == nil || !strings.Contains(err.Error(), "missing identification") {
		t.Fatalf("process: %v", err)
	}
	remove := newCacheRemoveCmd()
	got = captureStdout(t, func() {
		if err := remove.RunE(remove, []string{"1", "aaa", "1"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Count(got, "Removed cache entry") != 2 {
		t.Fatal(got)
	}
	if _, err := os.Stat(cfg.RipCacheDir()); err != nil {
		t.Fatal(err)
	}
	clear := newCacheClearCmd()
	if err := clear.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := clear.RunE(clear, nil); err != nil {
			t.Fatal(err)
		}
	})
	if entries, err := store.List(); err != nil || len(entries) != 0 {
		t.Fatalf("after clear: %+v %v", entries, err)
	}
}

func TestDiscIDCommandsAgainstTemporaryStore(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	list := newDiscIDListCmd()
	got := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "No disc ID cache entries") {
		t.Fatal(got)
	}
	store, err := discidcache.Open(cfg.DiscIDCachePath(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// The command must reject bad selectors without touching an existing cache.
	remove := newDiscIDRemoveCmd()
	for _, s := range []string{"bad", "0", "1"} {
		if err := remove.RunE(remove, []string{s}); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	if err := list.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	got = captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	var entries []discidcache.ListEntry
	if err := json.Unmarshal([]byte(got), &entries); err != nil || len(entries) != 0 {
		t.Fatalf("json: %s %v", got, err)
	}
	if err := store.Set("disc-long-identifier", discidcache.Entry{TMDBID: 42, MediaType: "tv", Title: "Example", Season: 2}); err != nil {
		t.Fatal(err)
	}
	if err := list.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	got = captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Example") || !strings.Contains(got, "S02") {
		t.Fatal(got)
	}
	got = captureStdout(t, func() {
		if err := remove.RunE(remove, []string{"1"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Removed: Example") || store.Size() != 1 {
		t.Fatalf("remove: %s; stale store size=%d", got, store.Size())
	}
	fresh, err := discidcache.Open(cfg.DiscIDCachePath(), nil)
	if err != nil || fresh.Size() != 0 {
		t.Fatalf("removed entry still on disk: %v %v", fresh, err)
	}
	if err := store.Set("another-disc", discidcache.Entry{TMDBID: 9, Title: "Other"}); err != nil {
		t.Fatal(err)
	}
	clear := newDiscIDClearCmd()
	if err := clear.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	got = captureStdout(t, func() {
		if err := clear.RunE(clear, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "All disc ID cache entries removed") {
		t.Fatal(got)
	}
}

func TestStoppedDaemonCommands(t *testing.T) {
	oldCfg, oldSocket, oldVerbose := cfg, flagSocket, flagVerbose
	t.Cleanup(func() { cfg, flagSocket, flagVerbose = oldCfg, oldSocket, oldVerbose })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir, LibraryDir: dir}, Library: config.LibraryConfig{MoviesDir: "movies", TVDir: "tv"}}
	flagSocket = filepath.Join(dir, "absent.sock")
	flagVerbose = false
	stop := newStopCmd()
	got := captureStdout(t, func() {
		if err := stop.RunE(stop, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "not running") {
		t.Fatal(got)
	}
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewDisc("Test", "fingerprint"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	status := newStatusCmd()
	got = captureStdout(t, func() {
		if err := status.RunE(status, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Spindle Status") || !strings.Contains(got, "identification") {
		t.Fatal(got)
	}
	flagVerbose = true
	got = captureStdout(t, func() {
		if err := status.RunE(status, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Socket") || !strings.Contains(got, "TV") {
		t.Fatal(got)
	}
	if err := status.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	got = captureStdout(t, func() {
		if err := status.RunE(status, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !json.Valid([]byte(got)) || !strings.Contains(got, "queueStats") {
		t.Fatal(got)
	}
}

func TestLogsCommandStoppedDaemon(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "missing.sock")
	path := cfg.DaemonLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newLogsCmd()
	if err := cmd.Flags().Set("lines", "2"); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(got, "first") || !strings.Contains(got, "second\nthird") {
		t.Fatal(got)
	}
	if err := cmd.Flags().Set("level", "warn"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "filters require") {
		t.Fatalf("filtered stopped daemon: %v", err)
	}
	if err := cmd.Flags().Set("level", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "read logs") {
		t.Fatalf("missing log: %v", err)
	}
	got = captureStdout(t, func() {
		printLogEntry(queueaccess.LogEntry{Time: "now", Level: "INFO", Msg: "message", Stage: "encoding", ItemID: 7, Fields: map[string]string{"key": "value"}})
	})
	for _, want := range []string{"now INFO message", "item_id=7", "stage=encoding", "key=value"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log output %q missing %q", got, want)
		}
	}
}

func TestSubtitleIdentityAndCommandPreflight(t *testing.T) {
	id, err := resolveSubtitleIdentity("/no-marker/movie.mkv", 42, 0, 0)
	if err != nil || id.TMDBID != 42 {
		t.Fatalf("explicit identity: %+v %v", id, err)
	}
	if _, err := resolveSubtitleIdentity("/no-marker/movie.mkv", 0, 0, 0); err == nil {
		t.Fatal("missing identity accepted")
	}
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg = &config.Config{}
	cmd := newGensubtitleCmd()
	if err := cmd.RunE(cmd, []string{"/nonexistent-file.mkv"}); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("missing file: %v", err)
	}
	file := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{file}); err == nil || !strings.Contains(err.Error(), "no [tmdbid-ID]") {
		t.Fatalf("missing marker: %v", err)
	}
}
