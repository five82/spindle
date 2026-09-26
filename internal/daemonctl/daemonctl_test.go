package daemonctl

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func TestIsRunning(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "daemon.lock")
	socketPath := filepath.Join(dir, "daemon.sock")
	if IsRunning(lockPath, socketPath) {
		t.Fatal("unlocked daemon reported running")
	}

	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	if IsRunning(lockPath, socketPath) {
		t.Fatal("locked daemon without socket reported running")
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if !IsRunning(lockPath, socketPath) {
		t.Fatal("locked daemon with reachable socket reported stopped")
	}

	// An invalid lock path must fail closed, not report a running daemon.
	if IsRunning(dir, socketPath) {
		t.Fatal("directory used as lock file reported running")
	}
}

func TestStartFailures(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "daemon.lock")
	socketPath := filepath.Join(dir, "daemon.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := Start(StartOptions{LockPath: lockPath, SocketPath: socketPath}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Start with running daemon = %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}

	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err = Start(StartOptions{LockPath: lockPath, SocketPath: socketPath, LogPath: filepath.Join(blocker, "log")})
	if err == nil || !strings.Contains(err.Error(), "create log directory") {
		t.Fatalf("Start with invalid log directory = %v", err)
	}

	// The directory exists but cannot be opened as a regular log file.
	err = Start(StartOptions{LockPath: lockPath, SocketPath: socketPath, LogPath: dir})
	if err == nil || !strings.Contains(err.Error(), "open daemon console log") {
		t.Fatalf("Start with directory as log = %v", err)
	}
}

func TestStartDetectsEarlyChildExitAndTruncatesConsoleLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "console.log")
	if err := os.WriteFile(logPath, []byte("old daemon output"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Start re-executes the test binary with `daemon`. The test binary has
	// no such mode, so it exits before the readiness poll instead of leaving
	// a background daemon behind.
	err := Start(StartOptions{
		LockPath: filepath.Join(dir, "lock"), SocketPath: filepath.Join(dir, "socket"),
		LogPath: logPath, ConfigFlag: filepath.Join(dir, "config.toml"),
	})
	if err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("early exit: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || strings.Contains(string(data), "old daemon output") {
		t.Fatalf("console log was not truncated: %q %v", data, err)
	}
}

func TestStopNotRunning(t *testing.T) {
	dir := t.TempDir()
	err := Stop(StopOptions{LockPath: filepath.Join(dir, "lock"), SocketPath: filepath.Join(dir, "socket")})
	if !errors.Is(err, ErrDaemonNotRunning) {
		t.Fatalf("Stop = %v, want ErrDaemonNotRunning", err)
	}
}

func TestStop(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "drain"
		if force {
			name = "force"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			lockPath := filepath.Join(dir, "daemon.lock")
			socketPath := filepath.Join(dir, "daemon.sock")
			lock := flock.New(lockPath)
			if err := lock.Lock(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Unlock() }()

			ln, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ln.Close() }()
			requests := make(chan *http.Request, 1)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r
				if err := lock.Unlock(); err != nil {
					t.Errorf("unlock: %v", err)
				}
				w.WriteHeader(http.StatusAccepted)
			})}
			go func() { _ = server.Serve(ln) }()
			defer func() { _ = server.Close() }()

			if err := Stop(StopOptions{LockPath: lockPath, SocketPath: socketPath, Token: "secret", Force: force}); err != nil {
				t.Fatalf("Stop = %v", err)
			}
			select {
			case req := <-requests:
				wantQuery := ""
				if force {
					wantQuery = "force=1"
				}
				if req.Method != http.MethodPost || req.URL.Path != "/api/daemon/stop" || req.URL.RawQuery != wantQuery || req.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("stop request = %s %s auth=%q", req.Method, req.URL.String(), req.Header.Get("Authorization"))
				}
			case <-time.After(time.Second):
				t.Fatal("no stop request received")
			}
		})
	}
}
