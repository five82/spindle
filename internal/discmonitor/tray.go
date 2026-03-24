//go:build linux

package discmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
	"unsafe"

	"github.com/five82/spindle/internal/logs"

	"golang.org/x/sys/unix"
)

// CD-ROM drive status ioctl and return codes.
const (
	cdromDriveStatus = 0x5326

	// Drive status codes returned by CDROM_DRIVE_STATUS ioctl.
	StatusNoInfo    = 0
	StatusNoDisk    = 1
	StatusTrayOpen  = 2
	StatusNotReady  = 3
	StatusDiscOK    = 4
)

// DriveStatus queries the current drive status via ioctl.
func DriveStatus(device string) (int, error) {
	fd, err := os.Open(device)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", device, err)
	}
	defer func() { _ = fd.Close() }()

	// ioctl(fd, CDROM_DRIVE_STATUS, 0)
	r1, _, errno := unix.Syscall(unix.SYS_IOCTL, fd.Fd(), cdromDriveStatus, uintptr(unsafe.Pointer(nil)))
	if errno != 0 {
		return -1, fmt.Errorf("ioctl CDROM_DRIVE_STATUS on %s: %w", device, errno)
	}
	return int(r1), nil
}

// WaitForReady polls the drive until it reports DiscOK or the context is
// cancelled. It polls up to 60 times at 1-second intervals.
func WaitForReady(ctx context.Context, device string, logger *slog.Logger) error {
	const maxPolls = 60
	const pollInterval = 1 * time.Second

	for i := range maxPolls {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		status, err := DriveStatus(device)
		if err != nil {
			return fmt.Errorf("drive status poll %d: %w", i+1, err)
		}

		if status == StatusDiscOK {
			logger.Info("drive ready",
				"decision_type", logs.DecisionDriveWait,
				"decision_result", "ready",
				"decision_reason", fmt.Sprintf("DiscOK after %d polls", i+1),
				"device", device,
			)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("drive %s not ready after %d polls", device, maxPolls)
}
