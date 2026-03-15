// Package daemonctl provides CLI-facing daemon control operations:
// checking if the daemon is running, starting/stopping it.
package daemonctl

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/gofrs/flock"
)

// ErrDaemonNotRunning is returned when the daemon is not running.
var ErrDaemonNotRunning = fmt.Errorf("daemon is not running")

// StartOptions configures how the daemon is launched.
type StartOptions struct {
	LockPath   string
	SocketPath string
	LogPath    string // Daemon stderr is redirected here.
	ConfigFlag string // If non-empty, passed as --config to the daemon.
}

// IsRunning checks if the daemon is running by testing the lock file
// and checking the socket.
func IsRunning(lockPath, socketPath string) bool {
	// Check lock file.
	fl := flock.New(lockPath)
	locked, err := fl.TryLock()
	if err != nil {
		return false
	}
	if locked {
		// We got the lock, so daemon is NOT running.
		_ = fl.Unlock()
		return false
	}
	// Lock is held by another process = daemon is running.

	// Also verify socket is reachable.
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Start launches the daemon as a background process. It resolves the
// current executable, spawns "spindle daemon" detached, and polls for
// readiness with a 10s timeout.
func Start(opts StartOptions) error {
	if IsRunning(opts.LockPath, opts.SocketPath) {
		return fmt.Errorf("daemon is already running")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	args := []string{"daemon"}
	if opts.ConfigFlag != "" {
		args = append(args, "--config", opts.ConfigFlag)
	}

	logFile, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}

	// Detach: we don't wait for the child.
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()

	// Poll for readiness (10s timeout, 500ms intervals).
	for range 20 {
		time.Sleep(500 * time.Millisecond)
		if IsRunning(opts.LockPath, opts.SocketPath) {
			return nil
		}
	}
	return fmt.Errorf("daemon did not become ready within 10 seconds")
}

// StopOptions configures how the daemon is stopped.
type StopOptions struct {
	LockPath   string
	SocketPath string
	Token      string // Bearer token for API authentication.
}

// Stop sends a stop request to the daemon via HTTP API and waits for it
// to shut down. Polls IsRunning() up to 10 seconds at 500ms intervals.
func Stop(opts StopOptions) error {
	if !IsRunning(opts.LockPath, opts.SocketPath) {
		return ErrDaemonNotRunning
	}

	// Send POST /api/daemon/stop via Unix socket.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", opts.SocketPath)
			},
		},
	}

	req, err := http.NewRequest(http.MethodPost, "http://localhost/api/daemon/stop", nil)
	if err != nil {
		return fmt.Errorf("create stop request: %w", err)
	}
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send stop request: %w", err)
	}
	_ = resp.Body.Close()

	// Poll for shutdown.
	for range 20 {
		time.Sleep(500 * time.Millisecond)
		if !IsRunning(opts.LockPath, opts.SocketPath) {
			return nil
		}
	}
	return fmt.Errorf("daemon did not stop within 10 seconds")
}
