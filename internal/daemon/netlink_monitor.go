package daemon

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/pilebones/go-udev/netlink"

	"spindle/internal/config"
	"spindle/internal/logging"
)

// netlinkMonitor listens for udev netlink events and triggers disc detection
// when a disc is inserted. This eliminates the need for udev rules that call
// the CLI as root.
type netlinkMonitor struct {
	cfg      *config.Config
	logger   *slog.Logger
	handler  func(ctx context.Context, device string) (*DiscDetectedResult, error)
	isPaused func() bool
	device   string

	mu      sync.Mutex
	conn    *netlink.UEventConn
	quit    chan struct{}
	running bool
}

// newNetlinkMonitor creates a netlink monitor that listens for disc insertion events.
func newNetlinkMonitor(
	cfg *config.Config,
	logger *slog.Logger,
	handler func(ctx context.Context, device string) (*DiscDetectedResult, error),
	isPaused func() bool,
) *netlinkMonitor {
	if cfg == nil {
		return nil
	}

	device := strings.TrimSpace(cfg.MakeMKV.OpticalDrive)
	if device == "" {
		return nil
	}

	return &netlinkMonitor{
		cfg:      cfg,
		logger:   logging.NewComponentLogger(logger, "netlink-monitor"),
		handler:  handler,
		isPaused: isPaused,
		device:   device,
	}
}

// Start begins listening for udev netlink events.
func (m *netlinkMonitor) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return nil
	}

	conn := new(netlink.UEventConn)
	if err := conn.Connect(netlink.UdevEvent); err != nil {
		m.logger.Warn("failed to connect to netlink socket; disc detection will rely on manual triggers",
			logging.Error(err),
			logging.String(logging.FieldEventType, "netlink_connect_failed"),
			logging.String(logging.FieldErrorHint, "ensure the daemon has permission to access netlink sockets"),
			logging.String(logging.FieldImpact, "automatic disc detection unavailable"),
		)
		return nil // Non-fatal - daemon can still function with manual detection
	}

	m.conn = conn
	m.quit = make(chan struct{})
	m.running = true

	// Pass quit channel to goroutine to avoid reading m.quit without lock
	quit := m.quit
	go m.monitorLoop(ctx, quit)

	m.logger.Info("netlink monitor started",
		logging.String(logging.FieldEventType, "netlink_monitor_started"),
		logging.String("device", m.device),
	)

	return nil
}

// Stop shuts down the netlink monitor.
func (m *netlinkMonitor) Stop() {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	if m.quit != nil {
		close(m.quit)
		m.quit = nil
	}

	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}

	m.running = false

	m.logger.Info("netlink monitor stopped",
		logging.String(logging.FieldEventType, "netlink_monitor_stopped"),
	)
}

// Running reports whether the netlink monitor is active.
func (m *netlinkMonitor) Running() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// monitorLoop reads netlink events and processes disc insertions.
func (m *netlinkMonitor) monitorLoop(ctx context.Context, quit <-chan struct{}) {
	queue := make(chan netlink.UEvent)
	errs := make(chan error)

	// Build matcher for disc events:
	// SUBSYSTEM=block, ID_CDROM=1, ID_CDROM_MEDIA=1, ACTION=change|add
	matcher := m.buildMatcher()

	// Start the go-udev monitor
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()

	if conn == nil {
		return
	}

	monitorQuit := conn.Monitor(queue, errs, matcher)

	for {
		select {
		case <-ctx.Done():
			close(monitorQuit)
			return
		case <-quit:
			close(monitorQuit)
			return
		case uevent := <-queue:
			m.handleEvent(ctx, uevent)
		case err := <-errs:
			m.logger.Warn("netlink monitor error",
				logging.Error(err),
				logging.String(logging.FieldEventType, "netlink_monitor_error"),
				logging.String(logging.FieldErrorHint, "check kernel netlink subsystem"),
				logging.String(logging.FieldImpact, "disc detection may be affected"),
			)
		}
	}
}

// buildMatcher creates a matcher for disc insertion events.
// Matches: SUBSYSTEM=block, ID_CDROM=1, ID_CDROM_MEDIA=1, ACTION=change|add
func (m *netlinkMonitor) buildMatcher() netlink.Matcher {
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

// handleEvent processes a matched uevent.
func (m *netlinkMonitor) handleEvent(ctx context.Context, uevent netlink.UEvent) {
	devname := m.extractDeviceName(uevent)
	if devname == "" {
		m.logger.Debug("ignoring event without device name",
			logging.String("action", string(uevent.Action)),
			logging.String("kobj", uevent.KObj),
		)
		return
	}

	if devname != m.device {
		m.logger.Debug("ignoring event for non-configured device",
			logging.String("device", devname),
			logging.String("configured_device", m.device),
		)
		return
	}

	if m.isPaused != nil && m.isPaused() {
		m.logger.Debug("disc detection paused, ignoring netlink event",
			logging.String("device", devname),
		)
		return
	}

	m.logger.Info("disc media detected via netlink",
		logging.String(logging.FieldEventType, "netlink_disc_detected"),
		logging.String("device", devname),
		logging.String("action", string(uevent.Action)),
	)

	if m.handler == nil {
		return
	}

	result, err := m.handler(ctx, devname)
	if err != nil {
		m.logger.Warn("netlink disc detection handler failed",
			logging.Error(err),
			logging.String("device", devname),
			logging.String(logging.FieldEventType, "netlink_handler_failed"),
			logging.String(logging.FieldErrorHint, "check disc monitor logs for details"),
			logging.String(logging.FieldImpact, "disc not queued"),
		)
		return
	}

	if result == nil {
		return
	}

	if result.Handled {
		m.logger.Info("disc queued via netlink detection",
			logging.String("device", devname),
			logging.String("message", result.Message),
			logging.Int64(logging.FieldItemID, result.ItemID),
			logging.String(logging.FieldEventType, "netlink_disc_queued"),
		)
	} else {
		m.logger.Debug("disc not handled",
			logging.String("device", devname),
			logging.String("message", result.Message),
		)
	}
}

// extractDeviceName gets the device path from a uevent.
func (m *netlinkMonitor) extractDeviceName(uevent netlink.UEvent) string {
	if devname := uevent.Env["DEVNAME"]; devname != "" {
		return devname
	}

	// Try to construct from DEVPATH (e.g., /devices/pci.../block/sr0)
	devpath := uevent.Env["DEVPATH"]
	if devpath == "" {
		return ""
	}

	parts := strings.Split(devpath, "/")
	if len(parts) == 0 {
		return ""
	}
	return "/dev/" + parts[len(parts)-1]
}
