//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestDetectAndEnqueueFromProbedDVD(t *testing.T) {
	mount := t.TempDir()
	if err := os.Mkdir(filepath.Join(mount, "VIDEO_TS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "VIDEO_TS", "VIDEO_TS.IFO"), []byte("disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"01_TEST_FILM\",\"fstype\":\"iso9660\",\"mountpoint\":\"" + mount + "\"}]}'\n"
	if err := os.WriteFile(filepath.Join(bin, "lsblk"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	m := New("/dev/sr0", store, nil, nil)
	for i := 0; i < 2; i++ {
		result, err := m.DetectAndEnqueue(context.Background())
		if err != nil || result == nil || result.Duplicate != (i == 1) || result.Item.DiscTitle != "TEST FILM" {
			t.Fatalf("pass %d: %+v, %v", i, result, err)
		}
	}
	m.PauseDisc()
	if result, err := m.DetectAndEnqueue(context.Background()); err != nil || result != nil {
		t.Fatalf("paused: %+v %v", result, err)
	}
	m.ResumeDisc()
}
