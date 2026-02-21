package daemon

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/pilebones/go-udev/netlink"

	"spindle/internal/config"
)

func TestNewNetlinkMonitor(t *testing.T) {
	t.Run("nil config returns nil", func(t *testing.T) {
		m := newNetlinkMonitor(nil, nil, nil, nil)
		if m != nil {
			t.Error("expected nil monitor for nil config")
		}
	})

	t.Run("empty optical drive returns nil", func(t *testing.T) {
		cfg := &config.Config{}
		m := newNetlinkMonitor(cfg, nil, nil, nil)
		if m != nil {
			t.Error("expected nil monitor for empty optical drive")
		}
	})

	t.Run("valid config creates monitor", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"
		m := newNetlinkMonitor(cfg, nil, nil, nil)
		if m == nil {
			t.Fatal("expected non-nil monitor")
		}
		if m.device != "/dev/sr0" {
			t.Errorf("expected device /dev/sr0, got %s", m.device)
		}
	})
}

func TestNetlinkMonitorRunning(t *testing.T) {
	t.Run("nil monitor returns false", func(t *testing.T) {
		var m *netlinkMonitor
		if m.Running() {
			t.Error("expected Running() to return false for nil monitor")
		}
	})

	t.Run("unstarted monitor returns false", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"
		m := newNetlinkMonitor(cfg, nil, nil, nil)
		if m.Running() {
			t.Error("expected Running() to return false for unstarted monitor")
		}
	})
}

func TestNetlinkMonitorStopStartIdempotency(t *testing.T) {
	t.Run("stop on nil monitor is safe", func(t *testing.T) {
		var m *netlinkMonitor
		m.Stop() // must not panic
	})

	t.Run("start on nil monitor is safe", func(t *testing.T) {
		var m *netlinkMonitor
		if err := m.Start(context.Background()); err != nil {
			t.Fatalf("Start on nil monitor should return nil, got: %v", err)
		}
	})

	t.Run("stop on unstarted monitor is safe", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"
		m := newNetlinkMonitor(cfg, nil, nil, nil)
		m.Stop() // must not panic
		if m.Running() {
			t.Error("expected Running() to return false after Stop on unstarted monitor")
		}
	})

	t.Run("double stop is safe", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"
		m := newNetlinkMonitor(cfg, nil, nil, nil)
		m.Stop() // first stop on unstarted
		m.Stop() // second stop - must not panic
	})

	t.Run("start after stop without prior start is safe", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"
		m := newNetlinkMonitor(cfg, nil, nil, nil)
		m.Stop()
		// Start will try to connect to netlink (will fail in test env without privileges)
		// but should not panic or return a hard error (non-fatal by design)
		_ = m.Start(context.Background())
	})
}

func TestBuildMatcher(t *testing.T) {
	cfg := &config.Config{}
	cfg.MakeMKV.OpticalDrive = "/dev/sr0"
	m := newNetlinkMonitor(cfg, nil, nil, nil)

	matcher := m.buildMatcher()
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	// Test that matcher accepts valid disc events
	validEvent := netlink.UEvent{
		Action: netlink.CHANGE,
		Env: map[string]string{
			"SUBSYSTEM":      "block",
			"ID_CDROM":       "1",
			"ID_CDROM_MEDIA": "1",
		},
	}
	if !matcher.Evaluate(validEvent) {
		t.Error("expected matcher to accept valid disc event")
	}

	// Test ADD action also matches
	addEvent := netlink.UEvent{
		Action: netlink.ADD,
		Env: map[string]string{
			"SUBSYSTEM":      "block",
			"ID_CDROM":       "1",
			"ID_CDROM_MEDIA": "1",
		},
	}
	if !matcher.Evaluate(addEvent) {
		t.Error("expected matcher to accept ADD action")
	}

	// Test that matcher rejects non-disc events
	nonDiscEvent := netlink.UEvent{
		Action: netlink.CHANGE,
		Env: map[string]string{
			"SUBSYSTEM": "block",
			"ID_CDROM":  "1",
			// Missing ID_CDROM_MEDIA
		},
	}
	if matcher.Evaluate(nonDiscEvent) {
		t.Error("expected matcher to reject event without ID_CDROM_MEDIA")
	}

	// Test REMOVE action doesn't match
	removeEvent := netlink.UEvent{
		Action: netlink.REMOVE,
		Env: map[string]string{
			"SUBSYSTEM":      "block",
			"ID_CDROM":       "1",
			"ID_CDROM_MEDIA": "1",
		},
	}
	if matcher.Evaluate(removeEvent) {
		t.Error("expected matcher to reject REMOVE action")
	}
}

func TestHandleEvent(t *testing.T) {
	t.Run("ignores event without device name", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"

		var handlerCalled bool
		handler := func(ctx context.Context, device string) (*DiscDetectedResult, error) {
			handlerCalled = true
			return &DiscDetectedResult{Handled: true}, nil
		}

		m := newNetlinkMonitor(cfg, nil, handler, nil)
		m.handleEvent(context.Background(), netlink.UEvent{
			Action: netlink.CHANGE,
			Env:    map[string]string{},
		})

		if handlerCalled {
			t.Error("handler should not be called for event without device name")
		}
	})

	t.Run("ignores event for non-configured device", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"

		var handlerCalled bool
		handler := func(ctx context.Context, device string) (*DiscDetectedResult, error) {
			handlerCalled = true
			return &DiscDetectedResult{Handled: true}, nil
		}

		m := newNetlinkMonitor(cfg, nil, handler, nil)
		m.handleEvent(context.Background(), netlink.UEvent{
			Action: netlink.CHANGE,
			Env: map[string]string{
				"DEVNAME": "/dev/sr1",
			},
		})

		if handlerCalled {
			t.Error("handler should not be called for non-configured device")
		}
	})

	t.Run("ignores event when paused", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"

		var handlerCalled bool
		handler := func(ctx context.Context, device string) (*DiscDetectedResult, error) {
			handlerCalled = true
			return &DiscDetectedResult{Handled: true}, nil
		}

		isPaused := func() bool { return true }

		m := newNetlinkMonitor(cfg, nil, handler, isPaused)
		m.handleEvent(context.Background(), netlink.UEvent{
			Action: netlink.CHANGE,
			Env: map[string]string{
				"DEVNAME": "/dev/sr0",
			},
		})

		if handlerCalled {
			t.Error("handler should not be called when paused")
		}
	})

	t.Run("calls handler for valid event", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"

		var handlerCalled bool
		var receivedDevice string
		handler := func(ctx context.Context, device string) (*DiscDetectedResult, error) {
			handlerCalled = true
			receivedDevice = device
			return &DiscDetectedResult{Handled: true, Message: "queued"}, nil
		}

		isPaused := func() bool { return false }

		m := newNetlinkMonitor(cfg, nil, handler, isPaused)
		m.handleEvent(context.Background(), netlink.UEvent{
			Action: netlink.CHANGE,
			Env: map[string]string{
				"DEVNAME": "/dev/sr0",
			},
		})

		if !handlerCalled {
			t.Error("handler should be called for valid event")
		}
		if receivedDevice != "/dev/sr0" {
			t.Errorf("expected device /dev/sr0, got %s", receivedDevice)
		}
	})

	t.Run("extracts device from DEVPATH when DEVNAME missing", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"

		var receivedDevice string
		handler := func(ctx context.Context, device string) (*DiscDetectedResult, error) {
			receivedDevice = device
			return &DiscDetectedResult{Handled: true}, nil
		}

		m := newNetlinkMonitor(cfg, nil, handler, func() bool { return false })
		m.handleEvent(context.Background(), netlink.UEvent{
			Action: netlink.CHANGE,
			Env: map[string]string{
				"DEVPATH": "/devices/pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0/block/sr0",
			},
		})

		if receivedDevice != "/dev/sr0" {
			t.Errorf("expected device /dev/sr0 from DEVPATH, got %s", receivedDevice)
		}
	})

	t.Run("respects dynamic pause state", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"

		var callCount int
		handler := func(ctx context.Context, device string) (*DiscDetectedResult, error) {
			callCount++
			return &DiscDetectedResult{Handled: true}, nil
		}

		var paused atomic.Bool
		isPaused := func() bool { return paused.Load() }

		m := newNetlinkMonitor(cfg, nil, handler, isPaused)
		event := netlink.UEvent{
			Action: netlink.CHANGE,
			Env: map[string]string{
				"DEVNAME": "/dev/sr0",
			},
		}

		// First call - not paused
		m.handleEvent(context.Background(), event)
		if callCount != 1 {
			t.Errorf("expected 1 call, got %d", callCount)
		}

		// Second call - paused
		paused.Store(true)
		m.handleEvent(context.Background(), event)
		if callCount != 1 {
			t.Errorf("expected still 1 call after pause, got %d", callCount)
		}

		// Third call - resumed
		paused.Store(false)
		m.handleEvent(context.Background(), event)
		if callCount != 2 {
			t.Errorf("expected 2 calls after resume, got %d", callCount)
		}
	})
}
