package daemonrun

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/gofrs/flock"
)

func TestRunRejectsOccupiedLockAndInvalidTCPBind(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *config.Config)
		want    string
	}{
		{"locked", func(t *testing.T, cfg *config.Config) {
			lock := flock.New(cfg.LockPath())
			if err := os.MkdirAll(filepath.Dir(cfg.LockPath()), 0o755); err != nil {
				t.Fatal(err)
			}
			ok, err := lock.TryLock()
			if err != nil || !ok {
				t.Fatalf("lock: %v %t", err, ok)
			}
			t.Cleanup(func() { _ = lock.Unlock() })
		}, "another daemon instance"},
		{"bad bind", func(t *testing.T, cfg *config.Config) { cfg.API.Bind = "invalid-bind-address" }, "start tcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}}
			tc.prepare(t, cfg)
			err := Run(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run: %v", err)
			}
		})
	}
}
