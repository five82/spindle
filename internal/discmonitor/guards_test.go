//go:build linux

package discmonitor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestDetectionGuards(t *testing.T) {
	m := New("", nil, nil, nil)
	if m.Device() != "/dev/sr0" {
		t.Fatalf("default device = %q", m.Device())
	}
	if skip, err := m.acquireForDetection(); err != nil || skip != "" {
		t.Fatalf("first acquisition = %q, %v", skip, err)
	}
	if skip, err := m.acquireForDetection(); err != nil || skip != "already processing a disc" {
		t.Fatalf("second acquisition = %q, %v", skip, err)
	}
	m.releaseProcessing()
	if !m.PauseDisc() {
		t.Fatal("could not pause monitor")
	}
	if skip, err := m.acquireForDetection(); err != nil || skip != "disc detection paused" {
		t.Fatalf("paused acquisition = %q, %v", skip, err)
	}
	m.ResumeDisc()
	if skip, err := m.acquireForDetection(); err != nil || skip != "" {
		t.Fatalf("acquisition after resume = %q, %v", skip, err)
	}
	m.releaseProcessing()
}

func TestDetectionGuardStoreFailure(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New("/dev/not-a-drive", store, nil, nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if skip, err := m.acquireForDetection(); err == nil || !strings.Contains(err.Error(), "check disc dependent items") || skip != "" {
		t.Fatalf("closed store guard = %q, %v", skip, err)
	}
}

func TestPausedDetectionDoesNotProbeDrive(t *testing.T) {
	m := New("/dev/not-a-drive", nil, nil, nil)
	m.PauseDisc()
	event, err := m.Detect(context.Background())
	if err != nil || event != nil {
		t.Fatalf("Detect while paused = %+v, %v", event, err)
	}
	result, err := m.DetectAsync(context.Background())
	if err != nil || result == nil || result.Handled || result.Message != "disc detection paused" {
		t.Fatalf("DetectAsync while paused = %+v, %v", result, err)
	}
	result2, err := m.DetectAndEnqueue(context.Background())
	if err != nil || result2 != nil {
		t.Fatalf("DetectAndEnqueue while paused = %+v, %v", result2, err)
	}
}
