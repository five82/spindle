package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/gofrs/flock"
)

func TestRunningDaemonGuardsManualOperations(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", flagSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	lock := flock.New(cfg.LockPath())
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	got := captureStdout(t, func() {
		if err := newStartCmd().RunE(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "already running") {
		t.Fatal(got)
	}
	if err := newCacheRipCmd().RunE(nil, nil); err == nil || !strings.Contains(err.Error(), "daemon is running") {
		t.Fatalf("rip guard: %v", err)
	}
	input := filepath.Join(dir, "input.mkv")
	if err := os.WriteFile(input, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newEncodeCmd().RunE(nil, []string{input}); err == nil || !strings.Contains(err.Error(), "daemon is running") {
		t.Fatalf("encode guard: %v", err)
	}
}
