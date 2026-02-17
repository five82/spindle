package fingerprint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
)

// runCommand executes a system command. It is a package-level variable so
// tests can replace it with a stub.
var runCommand = func(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// ensureMount returns the mount point for device, mounting it first if
// necessary. The weMounted return value indicates whether this function
// performed the mount (and the caller should unmount when done).
func ensureMount(ctx context.Context, device string) (mountPoint string, weMounted bool, err error) {
	// Already mounted?
	mountPoint, err = resolveMountPoint(device)
	if err != nil && !errors.Is(err, errMountNotFound) {
		return "", false, err
	}
	if mountPoint != "" {
		slog.Info("disc already mounted",
			"decision_type", "mount",
			"decision_result", "already_mounted",
			"decision_reason", "found in /proc/mounts",
			"mount_point", mountPoint,
		)
		return mountPoint, false, nil
	}

	// Fallback: disc structure at a well-known path?
	if mp := fallbackMountPoint(); mp != "" {
		slog.Info("disc found at fallback mount point",
			"decision_type", "mount",
			"decision_result", "fallback",
			"decision_reason", "disc structure found at well-known path",
			"mount_point", mp,
		)
		return mp, false, nil
	}

	// Mount it ourselves (fstab provides the mount point).
	slog.Info("mounting disc",
		"decision_type", "mount",
		"decision_result", "auto_mount",
		"decision_reason", "disc not already mounted",
		"device", device,
	)
	if err := runCommand(ctx, "mount", device); err != nil {
		return "", false, fmt.Errorf("mount %s: %w", device, err)
	}

	// Discover where fstab mounted it.
	mountPoint, err = resolveMountPoint(device)
	if err != nil || mountPoint == "" {
		// Mount succeeded but we can't find the mount point -- clean up.
		unmountDevice(ctx, device)
		return "", false, fmt.Errorf("mount %s succeeded but mount point not found", device)
	}

	slog.Info("disc mounted",
		"mount_point", mountPoint,
		"device", device,
	)
	return mountPoint, true, nil
}

// unmountDevice calls umount on device. Errors are logged but not returned
// since the fingerprint has already been computed by the time this runs.
func unmountDevice(ctx context.Context, device string) {
	slog.Info("unmounting disc", "device", device)
	if err := runCommand(ctx, "umount", device); err != nil {
		slog.Warn("failed to unmount disc",
			"event_type", "unmount_failed",
			"error_hint", "disc may still be mounted; manual umount may be needed",
			"impact", "disc remains mounted until manual intervention or eject",
			"device", device,
			"error", err,
		)
	}
}
