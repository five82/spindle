// Package daemonctl provides CLI-facing daemon control operations:
// checking if the daemon is running, and start/stop instructions.
package daemonctl

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gofrs/flock"
)

// ErrDaemonNotRunning is returned when the daemon is not running.
var ErrDaemonNotRunning = fmt.Errorf("daemon is not running")

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

// Start checks if the daemon is already running. If so, returns an error.
// Otherwise, returns an instruction to use `spindle daemon` or systemd.
func Start(lockPath, socketPath string) error {
	if IsRunning(lockPath, socketPath) {
		return fmt.Errorf("daemon is already running")
	}
	return fmt.Errorf("use 'spindle daemon' to start the daemon")
}

// Stop sends a stop request to the daemon via HTTP API and waits for it
// to shut down. Polls IsRunning() up to 10 seconds at 500ms intervals.
func Stop(lockPath, socketPath string) error {
	if !IsRunning(lockPath, socketPath) {
		return ErrDaemonNotRunning
	}

	// Send POST /api/daemon/stop via Unix socket.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}

	resp, err := client.Post("http://localhost/api/daemon/stop", "application/json", nil)
	if err != nil {
		return fmt.Errorf("send stop request: %w", err)
	}
	_ = resp.Body.Close()

	// Poll for shutdown.
	for range 20 {
		time.Sleep(500 * time.Millisecond)
		if !IsRunning(lockPath, socketPath) {
			return nil
		}
	}
	return fmt.Errorf("daemon did not stop within 10 seconds")
}
