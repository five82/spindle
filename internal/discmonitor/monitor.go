//go:build linux

package discmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/five82/spindle/internal/queue"
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
	device     string
	logger     *slog.Logger
	paused     atomic.Bool
	mu         sync.Mutex
	processing bool
	store      *queue.Store
}

// New creates a disc monitor for the given device.
func New(device string, logger *slog.Logger, store *queue.Store) *Monitor {
	if device == "" {
		device = "/dev/sr0"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		device: device,
		logger: logger,
		store:  store,
	}
}

// Device returns the optical drive device path.
func (m *Monitor) Device() string { return m.device }

// PauseDisc atomically pauses disc event processing.
// Returns true if the state changed (was not already paused).
func (m *Monitor) PauseDisc() bool { return m.paused.CompareAndSwap(false, true) }

// ResumeDisc atomically resumes disc event processing.
// Returns true if the state changed (was actually paused).
func (m *Monitor) ResumeDisc() bool { return m.paused.CompareAndSwap(true, false) }

// IsPaused returns whether the monitor is paused.
func (m *Monitor) IsPaused() bool { return m.paused.Load() }

// Detect wraps the full disc detection pipeline with concurrency guards.
// Returns nil event (not an error) if paused, already processing, or a
// disc-dependent item is in progress.
func (m *Monitor) Detect(ctx context.Context) (*DiscEvent, error) {
	if m.IsPaused() {
		m.logger.Info("disc detection skipped (paused)",
			"decision_type", "detect_guard",
			"decision_result", "skipped",
			"decision_reason", "monitor paused",
		)
		return nil, nil
	}

	if m.store != nil {
		busy, err := m.store.HasDiscDependentItem()
		if err != nil {
			return nil, fmt.Errorf("check disc dependent items: %w", err)
		}
		if busy {
			m.logger.Info("disc detection skipped (disc-dependent item in progress)",
				"decision_type", "detect_guard",
				"decision_result", "skipped",
				"decision_reason", "disc-dependent pipeline stage active",
			)
			return nil, nil
		}
	}

	m.mu.Lock()
	if m.processing {
		m.mu.Unlock()
		m.logger.Info("disc detection skipped (already processing)",
			"decision_type", "detect_guard",
			"decision_result", "skipped",
			"decision_reason", "detection already in progress",
		)
		return nil, nil
	}
	m.processing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.processing = false
		m.mu.Unlock()
	}()

	// Create a fingerprint context with 2-minute timeout.
	fpCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	event, err := ProbeDisc(fpCtx, m.device)
	if err != nil {
		return nil, err
	}

	return event, nil
}

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
