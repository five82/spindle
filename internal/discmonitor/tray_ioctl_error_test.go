//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDriveStatusIoctlFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DriveStatus(file); err == nil || !strings.Contains(err.Error(), "ioctl CDROM_DRIVE_STATUS") {
		t.Fatalf("regular file: %v", err)
	}
	if err := WaitForReady(context.Background(), file, nil); err == nil || !strings.Contains(err.Error(), "drive status poll 1") {
		t.Fatalf("poll: %v", err)
	}
}
