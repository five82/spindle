//go:build linux

package discmonitor

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/pilebones/go-udev/netlink"
)

func TestNetlinkDeviceFiltering(t *testing.T) {
	paused := false
	var handled []string
	n := NewNetlinkMonitor("/dev/sr0", func(_ context.Context, device string) {
		handled = append(handled, device)
	}, func() bool { return paused }, slog.Default())
	if n.buildMatcher() == nil {
		t.Fatal("missing netlink matcher")
	}
	for _, e := range []netlink.UEvent{
		{Env: map[string]string{}},
		{Env: map[string]string{"DEVNAME": "/dev/sr1"}},
	} {
		n.handleEvent(context.Background(), e)
	}
	paused = true
	n.handleEvent(context.Background(), netlink.UEvent{Env: map[string]string{"DEVNAME": "/dev/sr0"}})
	if len(handled) != 0 {
		t.Fatalf("ignored events dispatched: %v", handled)
	}
	paused = false
	n.handleEvent(context.Background(), netlink.UEvent{Env: map[string]string{"DEVPATH": "/devices/pci/block/sr0"}})
	if len(handled) != 1 || handled[0] != "/dev/sr0" {
		t.Fatalf("matching event dispatch = %v", handled)
	}
}

func TestNetlinkMonitorLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := NewNetlinkMonitor("/dev/sr0", func(context.Context, string) {
		t.Error("unexpected disc event")
	}, func() bool { return false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := n.Start(ctx); err != nil {
		// A sandbox may prohibit opening a netlink socket. Start must still
		// return the error rather than launching a broken monitor.
		if n.conn != nil {
			t.Fatal("failed start retained connection")
		}
		return
	}
	cancel()
	select {
	case <-n.done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not exit on cancellation")
	}
	n.Stop()
	n.Stop() // idempotent
	if n.conn != nil {
		t.Fatal("stopped monitor retained connection")
	}
}

func TestExtractDeviceName(t *testing.T) {
	for _, tt := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"DEVNAME": "/dev/sr2", "DEVPATH": "/devices/sr0"}, "/dev/sr2"},
		{map[string]string{"DEVPATH": "/devices/pci/block/sr0"}, "/dev/sr0"},
		{map[string]string{}, ""},
	} {
		if got := extractDeviceName(netlink.UEvent{Env: tt.env}); got != tt.want {
			t.Errorf("extractDeviceName(%v) = %q, want %q", tt.env, got, tt.want)
		}
	}
}
