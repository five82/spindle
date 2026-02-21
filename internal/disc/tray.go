package disc

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// ioctlCDROMDriveStatus is the Linux ioctl number for CDROM_DRIVE_STATUS.
const ioctlCDROMDriveStatus = 0x5326

// DriveStatus represents the result of a CDROM_DRIVE_STATUS ioctl call.
type DriveStatus int

const (
	DriveStatusNoInfo   DriveStatus = 0
	DriveStatusNoDisc   DriveStatus = 1
	DriveStatusTrayOpen DriveStatus = 2
	DriveStatusNotReady DriveStatus = 3
	DriveStatusDiscOK   DriveStatus = 4
)

// String returns a human-readable label for the drive status.
func (s DriveStatus) String() string {
	switch s {
	case DriveStatusNoInfo:
		return "no_info"
	case DriveStatusNoDisc:
		return "no_disc"
	case DriveStatusTrayOpen:
		return "tray_open"
	case DriveStatusNotReady:
		return "not_ready"
	case DriveStatusDiscOK:
		return "disc_ok"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// CheckDriveStatus queries the drive state using the CDROM_DRIVE_STATUS ioctl.
// Returns an error if the device cannot be opened or the ioctl fails.
func CheckDriveStatus(devicePath string) (DriveStatus, error) {
	devicePath = strings.TrimSpace(devicePath)
	if devicePath == "" {
		return DriveStatusNoInfo, fmt.Errorf("empty device path")
	}

	fd, err := syscall.Open(devicePath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return DriveStatusNoInfo, fmt.Errorf("open %s: %w", devicePath, err)
	}
	defer syscall.Close(fd) //nolint:errcheck

	r1, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(ioctlCDROMDriveStatus),
		uintptr(unsafe.Pointer(nil)),
	)
	if errno != 0 {
		return DriveStatusNoInfo, fmt.Errorf("ioctl CDROM_DRIVE_STATUS on %s: %w", devicePath, errno)
	}

	return DriveStatus(r1), nil
}

// WaitForReady polls the drive up to 60 times at 1-second intervals until
// it reports DriveStatusDiscOK or the context is cancelled.
func WaitForReady(ctx context.Context, devicePath string) (DriveStatus, error) {
	const (
		maxPolls     = 60
		pollInterval = 1 * time.Second
	)

	var lastStatus DriveStatus
	for i := 0; i < maxPolls; i++ {
		status, err := CheckDriveStatus(devicePath)
		if err != nil {
			return status, err
		}
		lastStatus = status
		if status == DriveStatusDiscOK {
			return status, nil
		}

		select {
		case <-ctx.Done():
			return lastStatus, ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return lastStatus, fmt.Errorf("drive %s not ready after %d polls (last status: %s)", devicePath, maxPolls, lastStatus)
}
