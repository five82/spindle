//go:build linux

package discmonitor

import (
	"context"
	"log/slog"
	"strings"

	"github.com/five82/spindle/internal/logs"

	"github.com/pilebones/go-udev/netlink"
)

// NetlinkMonitor listens for udev netlink events indicating disc insertion.
type NetlinkMonitor struct {
	device   string
	handler  func(ctx context.Context, device string)
	isPaused func() bool
	logger   *slog.Logger
	conn     *netlink.UEventConn
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

// Start opens a udev netlink connection and begins monitoring for disc events.
// Connection failure is non-fatal: logs a warning and returns an error.
func (n *NetlinkMonitor) Start(ctx context.Context) error {
	conn := new(netlink.UEventConn)
	if err := conn.Connect(netlink.UdevEvent); err != nil {
		n.logger.Warn("netlink socket creation failed",
			"event_type", "netlink_error",
			"error_hint", err.Error(),
			"impact", "automatic disc detection unavailable",
		)
		return err
	}

	n.conn = conn
	go n.monitorLoop(ctx)
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

	if n.conn != nil {
		_ = n.conn.Close()
		n.conn = nil
	}
}

// monitorLoop reads udev netlink events and dispatches matching disc events.
func (n *NetlinkMonitor) monitorLoop(ctx context.Context) {
	defer close(n.done)

	queue := make(chan netlink.UEvent, 1)
	errs := make(chan error, 1)

	matcher := n.buildMatcher()
	monitorQuit := n.conn.Monitor(queue, errs, matcher)

	for {
		select {
		case <-n.quit:
			close(monitorQuit)
			return
		case <-ctx.Done():
			close(monitorQuit)
			return
		case uevent := <-queue:
			n.handleEvent(ctx, uevent)
		case err := <-errs:
			n.logger.Warn("netlink monitor error",
				"event_type", "netlink_monitor_error",
				"error_hint", err.Error(),
				"impact", "disc detection may be affected",
			)
		}
	}
}

// buildMatcher creates a matcher for disc insertion events.
// Matches: SUBSYSTEM=block, ID_CDROM=1, ID_CDROM_MEDIA=1, ACTION=change|add
func (n *NetlinkMonitor) buildMatcher() netlink.Matcher {
	action := "change|add"
	rules := &netlink.RuleDefinitions{}
	rules.AddRule(netlink.RuleDefinition{
		Action: &action,
		Env: map[string]string{
			"SUBSYSTEM":      "block",
			"ID_CDROM":       "1",
			"ID_CDROM_MEDIA": "1",
		},
	})
	return rules
}

// handleEvent processes a matched uevent, filtering by device name.
func (n *NetlinkMonitor) handleEvent(ctx context.Context, uevent netlink.UEvent) {
	devname := extractDeviceName(uevent)
	if devname == "" {
		n.logger.Debug("ignoring event without device name",
			"action", string(uevent.Action),
			"kobj", uevent.KObj,
		)
		return
	}

	if devname != n.device {
		n.logger.Debug("ignoring event for non-configured device",
			"device", devname,
			"configured_device", n.device,
		)
		return
	}

	if n.isPaused() {
		n.logger.Info("disc event ignored (paused)",
			"decision_type", logs.DecisionDiscEventHandling,
			"decision_result", "skipped",
			"decision_reason", "paused",
			"device", n.device,
		)
		return
	}

	n.logger.Info("disc event detected via netlink",
		"event_type", "netlink_disc_event",
		"device", n.device,
		"action", string(uevent.Action),
	)
	n.handler(ctx, n.device)
}

// extractDeviceName gets the device path from a uevent.
// Falls back to constructing from DEVPATH when DEVNAME is absent (some kernel versions omit it).
func extractDeviceName(uevent netlink.UEvent) string {
	if devname := uevent.Env["DEVNAME"]; devname != "" {
		return devname
	}

	devpath := uevent.Env["DEVPATH"]
	if devpath == "" {
		return ""
	}

	return "/dev/" + devpath[strings.LastIndex(devpath, "/")+1:]
}
