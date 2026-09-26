//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAndEnqueueGuardsAndProbeError(t *testing.T) {
	m := New("/dev/sr0", nil, nil, nil)
	m.PauseDisc()
	result, err := m.DetectAndEnqueue(context.Background())
	if err != nil || result != nil {
		t.Fatalf("paused: %+v %v", result, err)
	}
	m.ResumeDisc()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lsblk"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err = m.DetectAndEnqueue(context.Background())
	if result != nil || err == nil || !strings.Contains(err.Error(), "lsblk probe") || m.processing {
		t.Fatalf("failed probe: %+v %v processing=%v", result, err, m.processing)
	}
}
