package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDerivedDurableAndDaemonPaths(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Paths: PathsConfig{StateDir: dir}}
	for _, tc := range []struct{ name, got, want string }{
		{"metrics", cfg.MetricsPath(), filepath.Join(dir, "metrics.jsonl")},
		{"log", cfg.DaemonLogPath(), filepath.Join(dir, "daemon.log")},
		{"log dir", cfg.DaemonLogDir(), dir},
		{"console", cfg.DaemonConsoleLogPath(), filepath.Join(dir, "daemon-console.log")},
	} {
		if tc.got != tc.want {
			t.Errorf("%s path = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if got := (MakeMKVConfig{KeyDBDownloadTimeout: 7}).KeyDBTimeout(); got != 7*time.Second {
		t.Fatalf("KeyDB timeout: %v", got)
	}
}
