//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/queue"
)

func TestDuplicateDecisionAndTitleRefresh(t *testing.T) {
	dir := t.TempDir()
	store, err := queue.Open(filepath.Join(dir, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Unknown Disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	m := New("/dev/sr0", store, nil, nil)
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	probe := filepath.Join(bin, "lsblk")
	writeProbe := func(body string) {
		t.Helper()
		if err := os.WriteFile(probe, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	event := &DiscEvent{Device: "/dev/sr0"}
	// An active duplicate never changes its title.
	m.logDuplicateDecision(context.Background(), item, event, "fp")
	if item.DiscTitle != "Unknown Disc" {
		t.Fatal(item.DiscTitle)
	}
	item.Stage = queue.StageCompleted
	writeProbe("exit 2")
	m.logDuplicateDecision(context.Background(), item, event, "fp")
	writeProbe(`echo '{"blockdevices":[{"name":"sr0","label":"VOLUME_ID"}]}'`)
	m.logDuplicateDecision(context.Background(), item, event, "fp")
	if item.DiscTitle != "Unknown Disc" {
		t.Fatal(item.DiscTitle)
	}
	writeProbe(`echo '{"blockdevices":[{"name":"sr0","label":"01_REAL_MOVIE"}]}'`)
	m.logDuplicateDecision(context.Background(), item, event, "fp")
	if item.DiscTitle != "REAL MOVIE" {
		t.Fatalf("title = %q", item.DiscTitle)
	}
	fresh, err := store.GetByID(item.ID)
	if err != nil || fresh.DiscTitle != "REAL MOVIE" {
		t.Fatalf("stored title: %+v %v", fresh, err)
	}
}

func TestDetectAsyncBackgroundEnqueue(t *testing.T) {
	dir := t.TempDir()
	mount := filepath.Join(dir, "mount")
	if err := os.Mkdir(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "disc.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "lsblk")
	data := `#!/bin/sh
printf '%s\n' '{"blockdevices":[{"name":"sr0","label":"TEST_DISC","mountpoint":"` + mount + `"}]}'
`
	if err := os.WriteFile(script, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := queue.Open(filepath.Join(dir, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := New("/dev/sr0", store, nil, nil)
	result, err := m.DetectAsync(context.Background())
	if err != nil || result == nil || !result.Handled || !strings.Contains(result.Message, "TEST_DISC") {
		t.Fatalf("detect: %+v %v", result, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		processing := m.processing
		m.mu.Unlock()
		if !processing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background enqueue did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].DiscTitle != "TEST DISC" {
		t.Fatalf("items: %+v %v", items, err)
	}
}
