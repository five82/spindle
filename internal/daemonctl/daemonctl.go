// Package daemonctl provides CLI-facing daemon control operations:
// checking if the daemon is running, and start/stop instructions.
package daemonctl

import (
	"fmt"
	"net"
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

// Start starts the daemon as a background process.
// (In practice, the user runs `spindle daemon` directly or via systemd.)
func Start(lockPath, socketPath string) error {
	if IsRunning(lockPath, socketPath) {
		return fmt.Errorf("daemon is already running")
	}
	return fmt.Errorf("use 'spindle daemon' to start the daemon")
}

// Stop stops the daemon by sending a signal.
func Stop(lockPath, socketPath string) error {
	if !IsRunning(lockPath, socketPath) {
		return ErrDaemonNotRunning
	}
	// In a real implementation, we'd send a signal or HTTP request.
	// For now, return an instruction.
	return fmt.Errorf("send SIGTERM to the daemon process to stop it")
}
