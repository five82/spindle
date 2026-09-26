package daemonrun

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
)

func TestCleanOldLogs(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "spindle-old.log")
	fresh := filepath.Join(dir, "spindle-new.log")
	other := filepath.Join(dir, "other.log")
	for _, path := range []string{old, fresh, other} {
		if err := os.WriteFile(path, []byte("log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().AddDate(0, 0, -40)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	cleanOldLogs(dir, 0) // default is 30 days
	for _, tc := range []struct {
		path string
		want bool
	}{
		{old, false}, {fresh, true}, {other, true},
	} {
		_, err := os.Stat(tc.path)
		if (err == nil) != tc.want {
			t.Errorf("Stat(%q) = %v, want exists=%v", tc.path, err, tc.want)
		}
	}
	// A non-directory should be harmless.
	cleanOldLogs(filepath.Join(dir, "missing"), 1)
}

func TestRunInvalidLogDirectory(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), &config.Config{Paths: config.PathsConfig{StateDir: filepath.Join(blocker, "state")}})
	if err == nil || !strings.Contains(err.Error(), "create log directory") {
		t.Fatalf("Run = %v, want log directory error", err)
	}
}

func TestRunCanceled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	originalLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: filepath.Join(dir, "state")}}
	if err := Run(ctx, cfg); err != nil {
		t.Fatalf("Run with canceled context = %v", err)
	}
	if _, err := os.Stat(cfg.SocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket after shutdown: %v, want not exist", err)
	}
	if _, err := os.Lstat(cfg.DaemonLogPath()); err != nil {
		t.Errorf("active log symlink: %v", err)
	}
}
