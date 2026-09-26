//go:build linux

package discmonitor

import (
	"context"
	"log/slog"
	"testing"

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
