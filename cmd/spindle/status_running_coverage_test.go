package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/gofrs/flock"
)

func TestStatusReadsRunningDaemonInsteadOfLocalQueue(t *testing.T) {
	oldCfg, oldSocket, oldVerbose, oldConfig := cfg, flagSocket, flagVerbose, flagConfig
	t.Cleanup(func() { cfg, flagSocket, flagVerbose, flagConfig = oldCfg, oldSocket, oldVerbose, oldConfig })
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir, LibraryDir: dir}, Library: config.LibraryConfig{MoviesDir: "Movies", TVDir: "TV"}}
	flagSocket = filepath.Join(dir, "api.sock")
	flagVerbose = true
	flagConfig = "operator.toml"
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	api := httpapi.New(httpapi.Params{Store: store, StatusInfo: httpapi.NewStatusInfo(cfg), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := api.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = api.Shutdown(context.Background()) }()
	lock := flock.New(cfg.LockPath())
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	cmd := newStatusCmd()
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"running", "Socket", "operator.toml", "Dependencies"} {
		if !strings.Contains(got, want) {
			t.Fatalf("running status missing %q: %s", want, got)
		}
	}
}
