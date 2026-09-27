package encoder

import (
	"bytes"
	"testing"
)

func TestPeakSecondBps(t *testing.T) {
	var buf bytes.Buffer
	if err := writeIVFHeader(&buf, 64, 64, 24, 1); err != nil {
		t.Fatalf("writeIVFHeader: %v", err)
	}
	// Second 0: 24 frames of 1000 bytes; second 1: 24 frames of 5000 bytes.
	payload := make([]byte, 5000)
	for pts := int64(0); pts < 48; pts++ {
		size := 1000
		if pts >= 24 {
			size = 5000
		}
		if err := writeIVFFrame(&buf, payload[:size], pts); err != nil {
			t.Fatalf("writeIVFFrame: %v", err)
		}
	}

	peak, err := PeakSecondBps(&buf, 24, 1)
	if err != nil {
		t.Fatalf("PeakSecondBps: %v", err)
	}
	if want := float64(24*5000) * 8; peak != want {
		t.Errorf("peak = %g bps, want %g", peak, want)
	}
}

func TestPeakSecondBpsRejectsBadInput(t *testing.T) {
	if _, err := PeakSecondBps(bytes.NewReader(nil), 24, 1); err == nil {
		t.Error("expected error for truncated IVF")
	}
	if _, err := PeakSecondBps(bytes.NewReader(make([]byte, 32)), 0, 1); err == nil {
		t.Error("expected error for zero frame rate")
	}
}
