package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
)

func TestStatusTrackerThroughStatusEndpoint(t *testing.T) {
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}}
	info := httpapi.NewStatusInfo(cfg)
	if info.QueueDBPath != cfg.QueueDBPath() || info.LockFilePath != cfg.LockPath() {
		t.Fatalf("paths: %+v", info)
	}
	tracker := httpapi.NewStatusTracker([]httpapi.DependencyResponse{{}})
	tracker.RecordFailure("rip failed")
	if last, deps := tracker.Snapshot(); last != "rip failed" || len(deps) != 1 {
		t.Fatalf("failure: %q %+v", last, deps)
	}
	srv := httpapi.New(httpapi.Params{Store: testStore(t), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), StatusInfo: info, StatusTracker: tracker})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "rip failed") || !strings.Contains(w.Body.String(), info.QueueDBPath) {
		t.Fatalf("status: %s", w.Body.String())
	}
	tracker.RecordSuccess()
	if last, _ := tracker.Snapshot(); last != "" {
		t.Fatalf("success did not clear error: %q", last)
	}
}

func TestUnixListenerAndShutdown(t *testing.T) {
	srv := httpapi.New(httpapi.Params{Store: testStore(t), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	path := filepath.Join(t.TempDir(), "api.sock")
	if err := srv.ListenUnix(path); err != nil {
		t.Fatal(err)
	}
	if err := srv.ListenUnix(path); err != nil {
		t.Fatalf("stale socket cleanup: %v", err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := srv.ListenUnix(filepath.Join(path, "impossible")); err == nil {
		t.Fatal("listen in socket accepted")
	}
}
