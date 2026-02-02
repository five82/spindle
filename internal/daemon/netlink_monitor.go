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

	monitorLogger := logger
	if monitorLogger != nil {
		monitorLogger = monitorLogger.With(logging.String("component", "netlink-monitor"))
	}

	return &netlinkMonitor{
		cfg:      cfg,
		logger:   monitorLogger,
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
		if m.logger != nil {
			m.logger.Warn("failed to connect to netlink socket; disc detection will rely on manual triggers",
				logging.Error(err),
				logging.String(logging.FieldEventType, "netlink_connect_failed"),
				logging.String(logging.FieldErrorHint, "ensure the daemon has permission to access netlink sockets"),
				logging.String(logging.FieldImpact, "automatic disc detection unavailable"),
			)
		}
		return nil // Non-fatal - daemon can still function with manual detection
	}

	m.conn = conn
	m.quit = make(chan struct{})
	m.running = true

	// Pass quit channel to goroutine to avoid reading m.quit without lock
	quit := m.quit
	go m.monitorLoop(ctx, quit)

	if m.logger != nil {
		m.logger.Info("netlink monitor started",
			logging.String(logging.FieldEventType, "netlink_monitor_started"),
			logging.String("device", m.device),
		)
	}

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

	if m.logger != nil {
		m.logger.Info("netlink monitor stopped",
			logging.String(logging.FieldEventType, "netlink_monitor_stopped"),
		)
	}
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
			if m.logger != nil {
				m.logger.Warn("netlink monitor error",
					logging.Error(err),
					logging.String(logging.FieldEventType, "netlink_monitor_error"),
				)
			}
		}
	}
}

// buildMatcher creates a matcher for disc insertion events.
func (m *netlinkMonitor) buildMatcher() netlink.Matcher {
	// Match disc media events:
	// - SUBSYSTEM=block
	// - ID_CDROM=1 (device is a CD-ROM)
	// - ID_CDROM_MEDIA=1 (disc has media loaded)
	// - ACTION=change or add
	action := "change|add"
	rule := netlink.RuleDefinition{
		Action: &action,
		Env: map[string]string{
			"SUBSYSTEM":      "block",
			"ID_CDROM":       "1",
			"ID_CDROM_MEDIA": "1",
		},
	}

	rules := &netlink.RuleDefinitions{}
	rules.AddRule(rule)
	return rules
}

// handleEvent processes a matched uevent.
func (m *netlinkMonitor) handleEvent(ctx context.Context, uevent netlink.UEvent) {
	// Extract device from event
	devname := uevent.Env["DEVNAME"]
	if devname == "" {
		// Try to construct from DEVPATH
		devpath := uevent.Env["DEVPATH"]
		if devpath != "" {
			// DEVPATH is like /devices/pci.../block/sr0
			parts := strings.Split(devpath, "/")
			if len(parts) > 0 {
				devname = "/dev/" + parts[len(parts)-1]
			}
		}
	}

	if devname == "" {
		if m.logger != nil {
			m.logger.Debug("ignoring event without device name",
				logging.String("action", string(uevent.Action)),
				logging.String("kobj", uevent.KObj),
			)
		}
		return
	}

	// Check if this is the configured device
	if devname != m.device {
		if m.logger != nil {
			m.logger.Debug("ignoring event for non-configured device",
				logging.String("device", devname),
				logging.String("configured_device", m.device),
			)
		}
		return
	}

	// Check if detection is paused
	if m.isPaused != nil && m.isPaused() {
		if m.logger != nil {
			m.logger.Debug("disc detection paused, ignoring netlink event",
				logging.String("device", devname),
			)
		}
		return
	}

	if m.logger != nil {
		m.logger.Info("disc media detected via netlink",
			logging.String(logging.FieldEventType, "netlink_disc_detected"),
			logging.String("device", devname),
			logging.String("action", string(uevent.Action)),
		)
	}

	// Call the handler
	if m.handler != nil {
		result, err := m.handler(ctx, devname)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("netlink disc detection handler failed",
					logging.Error(err),
					logging.String("device", devname),
					logging.String(logging.FieldEventType, "netlink_handler_failed"),
				)
			}
			return
		}

		if m.logger != nil {
			if result != nil && result.Handled {
				m.logger.Info("disc queued via netlink detection",
					logging.String("device", devname),
					logging.String("message", result.Message),
					logging.Int64(logging.FieldItemID, result.ItemID),
					logging.String(logging.FieldEventType, "netlink_disc_queued"),
				)
			} else if result != nil {
				m.logger.Debug("disc not handled",
					logging.String("device", devname),
					logging.String("message", result.Message),
				)
			}
		}
	}
}
