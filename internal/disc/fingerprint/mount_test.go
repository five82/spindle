package fingerprint

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEnsureMount_MountCalledAndCleanup(t *testing.T) {
	// /dev/sr99 won't be in /proc/mounts or fallback paths, so ensureMount
	// will call mount. After mount "succeeds", resolveMountPoint still won't
	// find it, so ensureMount must clean up with umount.
	var calls []string
	original := runCommand
	runCommand = func(_ context.Context, name string, _ ...string) error {
		calls = append(calls, name)
		return nil
	}
	defer func() { runCommand = original }()

	_, _, err := ensureMount(context.Background(), "/dev/sr99")
	if err == nil {
		t.Fatal("expected error when post-mount resolve fails")
	}
	if len(calls) != 2 || calls[0] != "mount" || calls[1] != "umount" {
		t.Fatalf("expected [mount, umount] calls, got %v", calls)
	}
	if !strings.Contains(err.Error(), "mount point not found") {
		t.Fatalf("error should mention mount point not found: %v", err)
	}
}

func TestEnsureMount_MountFailure(t *testing.T) {
	original := runCommand
	runCommand = func(_ context.Context, _ string, _ ...string) error {
		return errors.New("mount: no medium found")
	}
	defer func() { runCommand = original }()

	_, _, err := ensureMount(context.Background(), "/dev/sr99")
	if err == nil {
		t.Fatal("expected error on mount failure")
	}
	if !strings.Contains(err.Error(), "mount /dev/sr99") {
		t.Fatalf("error should reference device: %v", err)
	}
}

func TestUnmountDevice_Success(t *testing.T) {
	var called bool
	original := runCommand
	runCommand = func(_ context.Context, name string, _ ...string) error {
		if name != "umount" {
			t.Fatalf("expected umount, got %s", name)
		}
		called = true
		return nil
	}
	defer func() { runCommand = original }()

	unmountDevice(context.Background(), "/dev/sr0")
	if !called {
		t.Fatal("umount was not called")
	}
}

func TestUnmountDevice_FailureLogged(t *testing.T) {
	original := runCommand
	runCommand = func(_ context.Context, _ string, _ ...string) error {
		return errors.New("device busy")
	}
	defer func() { runCommand = original }()

	// Should not panic; error is logged, not returned.
	unmountDevice(context.Background(), "/dev/sr0")
}
