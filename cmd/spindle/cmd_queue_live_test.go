package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestQueueMutationsThroughDaemonSocket(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "api.sock")
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	item, err := store.NewDisc("Test", "fp")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(item.ID, 10)
	t.Setenv("TMPDIR", dir)
	audit := newQueueAuditCmd()
	if err := audit.RunE(audit, []string{"bad"}); err == nil || !strings.Contains(err.Error(), "invalid item ID") {
		t.Fatalf("bad audit id: %v", err)
	}
	if err := audit.RunE(audit, []string{"9999"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing audit item: %v", err)
	}
	var digest strings.Builder
	audit.SetOut(&digest)
	if err := audit.RunE(audit, []string{id}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(digest.String(), "Audit digest") {
		t.Fatal(digest.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "spindle-audit-"+id+".json")); err != nil {
		t.Fatal(err)
	}
	retry := newQueueRetryCmd()
	if got := captureStdout(t, func() {
		if err := retry.RunE(retry, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(got, "Retried 0") {
		t.Fatal(got)
	}
	if err := retry.RunE(retry, []string{id}); err == nil || !strings.Contains(err.Error(), "no failed items") {
		t.Fatalf("retry pending: %v", err)
	}
	if err := retry.RunE(retry, []string{"invalid"}); err == nil || !strings.Contains(err.Error(), "invalid item ID") {
		t.Fatalf("bad id: %v", err)
	}
	if err := retry.Flags().Set("episode", "missing"); err != nil {
		t.Fatal(err)
	}
	if err := retry.RunE(retry, nil); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("missing id: %v", err)
	}
	if err := retry.RunE(retry, []string{id}); err == nil || !strings.Contains(err.Error(), "not in failed state") {
		t.Fatalf("missing episode: %v", err)
	}
	if err := retry.RunE(retry, []string{"bad"}); err == nil || !strings.Contains(err.Error(), "invalid item ID") {
		t.Fatalf("bad episode item ID: %v", err)
	}
	if err := retry.RunE(retry, []string{"9999"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing episode item: %v", err)
	}
	if err := store.FailStage(item, queue.StageIdentification, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := retry.RunE(retry, []string{id}); err == nil || !strings.Contains(err.Error(), "episode missing not found") {
		t.Fatalf("missing episode on failed item: %v", err)
	}
	if err := retry.Flags().Set("episode", ""); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() {
		if err := retry.RunE(retry, []string{id}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(got, "Retried 1") {
		t.Fatalf("retry failed item: %s", got)
	}
	cancel := newQueueCancelCmd()
	if got := captureStdout(t, func() {
		if err := cancel.RunE(cancel, []string{id}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(got, "Canceled 1 item") {
		t.Fatal(got)
	}
	clear := newQueueClearCmd()
	if got := captureStdout(t, func() {
		if err := clear.RunE(clear, []string{id}); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(got, "Removed item") {
		t.Fatal(got)
	}
	completed := newQueueClearCmd()
	if err := completed.Flags().Set("completed", "true"); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() {
		if err := completed.RunE(completed, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(got, "Completed queue items removed") {
		t.Fatal(got)
	}
}
