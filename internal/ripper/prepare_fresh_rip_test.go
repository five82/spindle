package ripper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/stage"
)

func TestPrepareFreshRipResumesMonitorAfterReadinessFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	sess := &stage.Session{Logger: testLogger()}
	h := New(&config.Config{}, nil, nil, nil, NoTitleOverride)
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cleanup, err := h.prepareFreshRip(context.Background(), sess, filepath.Join(blocked, "ripped"))
	if err == nil || !strings.Contains(err.Error(), "create ripped dir") {
		t.Fatalf("blocked directory: %v", err)
	}
	cleanup()
	h.cfg.MakeMKV.OpticalDrive = "/dev/nonexistent-spindle-test"
	cleanup, err = h.prepareFreshRip(context.Background(), sess, filepath.Join(dir, "ripped"))
	if err == nil || !strings.Contains(err.Error(), "drive readiness") {
		t.Fatalf("missing drive: %v", err)
	}
	cleanup()
	h.cfg.MakeMKV.OpticalDrive = "disc:0"
	cleanup, err = h.prepareFreshRip(context.Background(), sess, filepath.Join(dir, "ripped"))
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(home, ".MakeMKV", "settings.conf")); err != nil {
		t.Fatalf("settings: %v", err)
	}
	monitor := discmonitor.New("disc:0", nil, nil, testLogger())
	h = New(&config.Config{MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0"}}, nil, nil, monitor, NoTitleOverride)
	cleanup, err = h.prepareFreshRip(context.Background(), sess, filepath.Join(dir, "with-monitor"))
	if err != nil || !monitor.IsPaused() {
		t.Fatalf("monitor not paused: %v", err)
	}
	cleanup()
	if monitor.IsPaused() {
		t.Fatal("monitor not resumed after rip")
	}
	h.cfg.MakeMKV.OpticalDrive = "/dev/not-a-physical-drive-spindle-test"
	cleanup, err = h.prepareFreshRip(context.Background(), sess, filepath.Join(dir, "not-ready"))
	if err == nil || monitor.IsPaused() {
		t.Fatalf("failed readiness must resume monitor: %v", err)
	}
	cleanup()
}
