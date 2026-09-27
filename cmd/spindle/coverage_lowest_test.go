package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
)

func TestCacheRipAndEncodePreflightErrors(t *testing.T) {
	oldCfg, oldSocket, oldQuiet := cfg, flagSocket, flagQuiet
	t.Cleanup(func() { cfg, flagSocket, flagQuiet = oldCfg, oldSocket, oldQuiet })
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "missing.sock")
	rip := newCacheRipCmd()
	for _, tc := range []struct {
		title, choose bool
		device, want  string
	}{
		{true, true, "/dev/missing", "cannot combine"},
		{false, false, "", "no device specified"},
		{false, false, "/dev/this-device-does-not-exist", "probe disc"},
	} {
		if err := rip.Flags().Set("title", map[bool]string{true: "2", false: "-1"}[tc.title]); err != nil {
			t.Fatal(err)
		}
		if err := rip.Flags().Set("choose", map[bool]string{true: "true", false: "false"}[tc.choose]); err != nil {
			t.Fatal(err)
		}
		var args []string
		if tc.device != "" {
			args = []string{tc.device}
		}
		if err := rip.RunE(rip, args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("rip %q: %v, want %s", tc.device, err, tc.want)
		}
	}
	encode := newEncodeCmd()
	if err := encode.RunE(encode, []string{filepath.Join(dir, "missing.mkv")}); err == nil || !strings.Contains(err.Error(), "input file") {
		t.Fatalf("missing encode input: %v", err)
	}
	input := filepath.Join(dir, "input.mkv")
	if err := os.WriteFile(input, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := encode.Flags().Set("output-dir", filepath.Join(blocker, "out")); err != nil {
		t.Fatal(err)
	}
	if err := encode.RunE(encode, []string{input}); err == nil || !strings.Contains(err.Error(), "create output dir") {
		t.Fatalf("blocked encode output: %v", err)
	}
}

func TestCacheCommandFailureBoundaries(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}, RipCache: config.RipCacheConfig{MaxGiB: 1}}
	store := ripcache.New(cfg.RipCacheDir(), 1)
	if err := store.WriteMetadata("abc123", ripcache.EntryMetadata{Fingerprint: "abc123", DiscTitle: "One"}); err != nil {
		t.Fatal(err)
	}
	if err := newCacheRemoveCmd().RunE(nil, []string{"absent"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("remove missing: %v", err)
	}
	if err := newCacheProcessCmd().RunE(nil, []string{"absent"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("process missing: %v", err)
	}
	if err := newCacheClearCmd().RunE(nil, nil); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("clear without confirmation: %v", err)
	}
	// A cache directory that is a regular file must fail listing, selection and deletion.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "blocked"))
	if err := os.MkdirAll(filepath.Dir(cfg.RipCacheDir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RipCacheDir(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() error{
		func() error { return newCacheListCmd().RunE(nil, nil) },
		func() error { _, err := cacheEntryByNumber(1); return err },
		func() error { _, err := cacheEntryBySelector("1"); return err },
		func() error { return newCacheRemoveCmd().RunE(nil, []string{"1"}) },
	} {
		if err := run(); err == nil {
			t.Error("cache listing unexpectedly succeeded")
		}
	}
}

func TestStagingCleanProtectsActiveFingerprint(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir, StagingDir: filepath.Join(dir, "staging")}}
	flagSocket = filepath.Join(dir, "api.sock")
	if err := os.MkdirAll(cfg.Paths.StagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.NewDisc("Active", "active-fp"); err != nil {
		t.Fatal(err)
	}
	api := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := api.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = api.Shutdown(context.Background()) }()
	for _, name := range []string{"active-fp", "orphan-fp"} {
		if err := os.Mkdir(filepath.Join(cfg.Paths.StagingDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := newStagingCleanCmd()
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Removed 1 staging directories") {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.StagingDir, "active-fp")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.StagingDir, "orphan-fp")); !os.IsNotExist(err) {
		t.Fatalf("orphan not removed: %v", err)
	}
	flagSocket = filepath.Join(dir, "missing.sock")
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("clean succeeded without daemon")
	}
}
