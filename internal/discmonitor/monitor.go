//go:build linux

package discmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// DiscEvent represents a disc insertion or removal event.
type DiscEvent struct {
	Device    string // e.g., "/dev/sr0"
	Label     string // disc label from lsblk
	DiscType  string // "Blu-ray", "DVD", "Unknown"
	MountPath string // mount point if mounted
}

// Monitor watches for optical disc events.
type Monitor struct {
	device string
	logger *slog.Logger
	paused bool
}

// New creates a disc monitor for the given device.
func New(device string, logger *slog.Logger) *Monitor {
	if device == "" {
		device = "/dev/sr0"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		device: device,
		logger: logger,
	}
}

// Pause temporarily stops disc event processing.
func (m *Monitor) Pause() { m.paused = true }

// Resume resumes disc event processing.
func (m *Monitor) Resume() { m.paused = false }

// IsPaused returns whether the monitor is paused.
func (m *Monitor) IsPaused() bool { return m.paused }

// ProbeDisc detects a loaded disc via lsblk and returns a DiscEvent.
// Returns nil if no disc is detected or lsblk reports no block devices.
func ProbeDisc(ctx context.Context, device string) (*DiscEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	//nolint:gosec // device path is validated by caller
	cmd := exec.CommandContext(ctx, "lsblk", "--json", "-o", "NAME,LABEL,FSTYPE,MOUNTPOINT", device)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk probe failed for %s: %w", device, err)
	}

	dev, err := parseLsblk(out)
	if err != nil {
		return nil, fmt.Errorf("parse lsblk output for %s: %w", device, err)
	}

	return &DiscEvent{
		Device:    device,
		Label:     strings.TrimSpace(dev.Label),
		DiscType:  classifyDisc(dev.FSType),
		MountPath: dev.MountPoint,
	}, nil
}

// EjectDisc ejects the disc in the given device.
func EjectDisc(ctx context.Context, device string) error {
	//nolint:gosec // device path is validated by caller
	cmd := exec.CommandContext(ctx, "eject", device)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("eject %s: %w", device, err)
	}
	return nil
}

// ValidateLabel checks that a disc label is non-empty and contains printable
// characters. Labels that are all whitespace or control characters are rejected.
func ValidateLabel(label string) bool {
	if label == "" {
		return false
	}
	for _, r := range label {
		if r > ' ' && r != 0x7f {
			return true
		}
	}
	return false
}
