package daemonrun

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
)

func TestRunStartupFailureBoundaries(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	for _, tc := range []string{"queue", "lock"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &config.Config{Paths: config.PathsConfig{StateDir: dir}}
			path := cfg.QueueDBPath()
			if tc == "lock" {
				path = cfg.LockPath()
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			err := Run(context.Background(), cfg)
			want := map[string]string{"queue": "open queue", "lock": "lock file"}[tc]
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Run = %v, want %s", err, want)
			}
		})
	}
}

func TestRunReportsBlockedUnixSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	if err := os.MkdirAll(cfg.SocketPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.SocketPath(), "preserve"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "start unix socket") {
		t.Fatalf("blocked socket: %v", err)
	}
}

func TestRunStopsOnHTTPForceRequest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, cfg) }()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(cfg.SocketPath()); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("daemon stopped before socket: %v", err)
		case <-deadline:
			t.Fatal("daemon socket unavailable")
		case <-time.After(10 * time.Millisecond):
		}
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.SocketPath())
	}}}
	resp, err := client.Post("http://spindle/api/daemon/stop?force=1", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status: %s", resp.Status)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := os.Stat(cfg.SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket not removed: %v", err)
	}
}

func TestRunKeepsUnreplaceableActiveLogPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	path := cfg.DaemonLogPath()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "preserve")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("existing log directory was removed: %v", err)
	}
}

func TestRunTogglesLogLevelOnSIGUSR1(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: filepath.Join(dir, "state")}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, cfg) }()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(cfg.SocketPath()); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("daemon failed to start: %v", err)
		case <-deadline:
			t.Fatal("daemon did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	for {
		data, err := os.ReadFile(cfg.DaemonLogPath())
		if err == nil && strings.Contains(string(data), "daemon started") {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("daemon stopped before ready: %v", err)
		case <-deadline:
			t.Fatal("daemon did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Both transitions must be logged while the daemon is serving, not merely
	// after shutdown when its signal handlers have been unregistered.
	for _, want := range []string{"raised to INFO", "lowered to DEBUG"} {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		for {
			data, err := os.ReadFile(cfg.DaemonLogPath())
			if err == nil && strings.Contains(string(data), want) {
				break
			}
			select {
			case err := <-finished:
				t.Fatalf("daemon stopped before %q: %v", want, err)
			case <-deadline:
				t.Fatalf("missing log transition %q", want)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

func TestRunWithOptionalServicesAndQueuedItem(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: filepath.Join(dir, "state")},
		RipCache: config.RipCacheConfig{Enabled: true, MaxGiB: 1}, DiscIDCache: config.DiscIDCacheConfig{Enabled: true},
		MakeMKV: config.MakeMKVConfig{OpticalDrive: "/dev/not-a-drive"}}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewDisc("Queued", "fp"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.DiscIDCachePath()); err != nil {
		t.Fatalf("optional disc cache not opened: %v", err)
	}
}
