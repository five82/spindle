//go:build linux

package discmonitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fallbackMountPaths are checked when lsblk reports no mount point.
var fallbackMountPaths = []string{"/media/cdrom", "/media/cdrom0"}

// resolveMountPoint finds or creates a mount point for the given device.
// It checks (in order): the provided lsblk mount path, /proc/mounts,
// fallback paths with disc directory structure, and finally auto-mounts.
// If auto-mounted, the returned cleanup function unmounts the device.
func resolveMountPoint(ctx context.Context, device, lsblkMount string) (mountPoint string, cleanup func(), err error) {
	noop := func() {}

	// 1. Use lsblk-provided mount path if non-empty.
	if lsblkMount != "" {
		if hasDiscStructure(lsblkMount) {
			return lsblkMount, noop, nil
		}
		// lsblk says it's mounted here but no disc structure; still use it.
		return lsblkMount, noop, nil
	}

	// 2. Check /proc/mounts for the device (symlink-aware).
	if mp, err := findInProcMounts(device); err == nil && mp != "" {
		return mp, noop, nil
	}

	// 3. Check fallback paths for disc directory structure.
	for _, path := range fallbackMountPaths {
		if hasDiscStructure(path) {
			return path, noop, nil
		}
	}

	// 4. Auto-mount via system mount command (fstab provides mount point).
	mp, err := autoMount(ctx, device)
	if err != nil {
		return "", noop, fmt.Errorf("auto-mount %s: %w", device, err)
	}
	return mp, func() {
		// Best-effort unmount; ignore errors.
		_ = exec.CommandContext(ctx, "umount", device).Run()
	}, nil
}

// findInProcMounts parses /proc/mounts for the given device and returns
// its mount point. Handles symlinks by resolving both device paths.
func findInProcMounts(device string) (string, error) {
	realDevice, err := filepath.EvalSymlinks(device)
	if err != nil {
		realDevice = device
	}

	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		mountDev := fields[0]
		mountPoint := fields[1]

		// Compare raw and resolved paths.
		if mountDev == device || mountDev == realDevice {
			return mountPoint, nil
		}
		if resolved, err := filepath.EvalSymlinks(mountDev); err == nil {
			if resolved == realDevice {
				return mountPoint, nil
			}
		}
	}
	return "", nil
}

// hasDiscStructure returns true if the path contains BDMV/ or VIDEO_TS/.
func hasDiscStructure(path string) bool {
	if info, err := os.Stat(filepath.Join(path, "BDMV")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(path, "VIDEO_TS")); err == nil && info.IsDir() {
		return true
	}
	return false
}

// autoMount runs `mount <device>` and determines the mount point from
// /proc/mounts afterwards. Requires fstab configuration.
func autoMount(ctx context.Context, device string) (string, error) {
	//nolint:gosec // device path is validated by caller
	cmd := exec.CommandContext(ctx, "mount", device)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("mount: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Read back mount point from /proc/mounts.
	mp, err := findInProcMounts(device)
	if err != nil {
		return "", fmt.Errorf("find mount after auto-mount: %w", err)
	}
	if mp == "" {
		return "", fmt.Errorf("device %s mounted but mount point not found in /proc/mounts", device)
	}
	return mp, nil
}
