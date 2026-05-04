package queueaccess

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenHTTPDaemonUnavailable(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	_, err := OpenHTTP(socketPath, "")
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("OpenHTTP error = %v, want ErrDaemonUnavailable", err)
	}
}
