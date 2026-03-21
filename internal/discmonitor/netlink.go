//go:build linux

package discmonitor

import (
	"context"
	"log/slog"
	"strings"

	"golang.org/x/sys/unix"
)

// NetlinkMonitor listens for udev/netlink events indicating disc insertion.
type NetlinkMonitor struct {
	device   string
	handler  func(ctx context.Context, device string)
	isPaused func() bool
	logger   *slog.Logger
	quit     chan struct{}
	done     chan struct{}
}

// NewNetlinkMonitor creates a new netlink-based disc event monitor.
func NewNetlinkMonitor(
	device string,
	handler func(ctx context.Context, device string),
	isPaused func() bool,
	logger *slog.Logger,
) *NetlinkMonitor {
	return &NetlinkMonitor{
		device:   device,
		handler:  handler,
		isPaused: isPaused,
		logger:   logger,
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start opens a netlink socket and begins monitoring for disc events.
// Connection failure is non-fatal: logs a warning and returns an error.
func (n *NetlinkMonitor) Start(ctx context.Context) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		n.logger.Warn("netlink socket creation failed",
			"event_type", "netlink_error",
			"error_hint", err.Error(),
			"impact", "automatic disc detection unavailable",
		)
		return err
	}

	addr := unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: 1, // multicast group for kernel events
	}
	if err := unix.Bind(fd, &addr); err != nil {
		_ = unix.Close(fd)
		n.logger.Warn("netlink bind failed",
			"event_type", "netlink_error",
			"error_hint", err.Error(),
			"impact", "automatic disc detection unavailable",
		)
		return err
	}

	go n.monitorLoop(ctx, fd)
	n.logger.Info("netlink monitor started", "device", n.device)
	return nil
}

// Stop closes the quit channel and waits for the monitor loop to finish.
func (n *NetlinkMonitor) Stop() {
	select {
	case <-n.quit:
		return // already stopped
	default:
		close(n.quit)
	}
	<-n.done
}

// monitorLoop reads netlink events and dispatches matching disc events.
func (n *NetlinkMonitor) monitorLoop(ctx context.Context, fd int) {
	defer close(n.done)
	defer func() { _ = unix.Close(fd) }()

	buf := make([]byte, 4096)
	// Extract expected device name (e.g., "sr0" from "/dev/sr0").
	expectedDev := n.device
	if idx := strings.LastIndex(n.device, "/"); idx >= 0 {
		expectedDev = n.device[idx+1:]
	}

	// Set read timeout to avoid blocking forever on Recvfrom.
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO,
		&unix.Timeval{Sec: 1})

	for {
		select {
		case <-n.quit:
			return
		case <-ctx.Done():
			return
		default:
		}

		nr, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			// Timeout or interrupted: just loop again.
			continue
		}
		if nr <= 0 {
			continue
		}

		// Parse NUL-separated uevent fields.
		fields := parseUevent(buf[:nr])

		// Filter for optical disc events.
		if !isOpticalDiscEvent(fields, expectedDev) {
			continue
		}

		if n.isPaused() {
			n.logger.Debug("disc event ignored (paused)",
				"event_type", "netlink_event_paused",
				"device", n.device,
			)
			continue
		}

		n.logger.Info("disc event detected via netlink",
			"event_type", "netlink_disc_event",
			"device", n.device,
			"action", fields["ACTION"],
		)
		n.handler(ctx, n.device)
	}
}

// parseUevent splits a NUL-separated uevent buffer into key=value pairs.
func parseUevent(data []byte) map[string]string {
	fields := make(map[string]string)
	for _, part := range strings.Split(string(data), "\x00") {
		if k, v, ok := strings.Cut(part, "="); ok {
			fields[k] = v
		}
	}
	return fields
}

// isOpticalDiscEvent checks if uevent fields match an optical disc insertion.
func isOpticalDiscEvent(fields map[string]string, expectedDev string) bool {
	action := fields["ACTION"]
	if action != "change" && action != "add" {
		return false
	}
	if fields["SUBSYSTEM"] != "block" {
		return false
	}
	if fields["ID_CDROM"] != "1" {
		return false
	}
	if fields["ID_CDROM_MEDIA"] != "1" {
		return false
	}
	// Check device name matches if specified.
	devname := fields["DEVNAME"]
	if expectedDev != "" && devname != "" {
		// DEVNAME may be just "sr0" or "/dev/sr0".
		devBase := devname
		if idx := strings.LastIndex(devname, "/"); idx >= 0 {
			devBase = devname[idx+1:]
		}
		if devBase != expectedDev {
			return false
		}
	}
	return true
}

