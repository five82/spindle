package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueaccess"
)

func TestLogsFollowReceivesNewEntriesAndStopsOnSignal(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "api.sock")
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	buf := httpapi.NewLogBuffer(10)
	buf.Append(httpapi.LogEntry{Time: "start", Level: "INFO", Msg: "initial entry"})
	server := httpapi.New(httpapi.Params{Store: store, LogBuffer: buf, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Shutdown(context.Background()) }()
	acc, err := queueaccess.OpenHTTP(flagSocket, "")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		buf.Append(httpapi.LogEntry{Time: "later", Level: "WARN", Msg: "followed entry"})
		time.Sleep(1300 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}()
	got := captureStdout(t, func() {
		if err := logsFromAPI(acc, queueaccess.LogsQuery{Limit: 10}, true); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"initial entry", "followed entry"} {
		if !strings.Contains(got, want) {
			t.Fatalf("follow output missing %q: %s", want, got)
		}
	}
}

func TestLogsCommandFiltersThroughDaemonAPI(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "api.sock")
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Test", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	buf := httpapi.NewLogBuffer(10)
	buf.Append(httpapi.LogEntry{Time: time.Now().UTC().Format(time.RFC3339Nano), Level: "WARN", Msg: "disk slow", Component: "ripper", ItemID: item.ID})
	buf.Append(httpapi.LogEntry{Time: time.Now().UTC().Format(time.RFC3339Nano), Level: "INFO", Msg: "other item", Component: "ripper", ItemID: 8})
	server := httpapi.New(httpapi.Params{Store: store, LogBuffer: buf, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := server.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Shutdown(context.Background()) }()
	cmd := newLogsCmd()
	for name, value := range map[string]string{"component": "ripper", "item": "1", "level": "warn", "lines": "1"} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "disk slow") || strings.Contains(got, "other item") {
		t.Fatalf("filtered log entries: %s", got)
	}
	acc, err := queueaccess.OpenHTTP(flagSocket, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := logsFromAPI(acc, queueaccess.LogsQuery{}, false); err == nil || !strings.Contains(err.Error(), "fetch logs") {
		t.Fatalf("fetch failure: %v", err)
	}
	flagSocket = filepath.Join(dir, "missing.sock")
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "filters require") {
		t.Fatalf("disconnected daemon: %v", err)
	}
}
