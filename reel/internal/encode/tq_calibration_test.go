package encode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/reel/internal/quality"
)

func TestSSIMU2CalibrationLocksAtMedianOffset(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tq"), 0755); err != nil {
		t.Fatal(err)
	}
	c := newSSIMU2Calibration(dir)
	if c.CurrentOffset() != 0 {
		t.Fatalf("offset before lock = %g, want 0", c.CurrentOffset())
	}
	// Samples at varying JOD, all sitting exactly 4.8 points below the
	// corpus anchor (a bts-like grainy title).
	jods := make([]float32, ssimu2CalibrationMinSamples)
	for i := range jods {
		jods[i] = 9.1 + 0.05*float32(i%15)
	}
	for i, jod := range jods {
		s2 := quality.SSIMU2FromJOD(jod) - 4.8
		offset, justLocked := c.AddSample(jod, s2)
		if i < len(jods)-1 && justLocked {
			t.Fatalf("locked early at sample %d", i+1)
		}
		if i == len(jods)-1 {
			if !justLocked {
				t.Fatal("did not lock at min samples")
			}
			if offset > -4.7 || offset < -4.9 {
				t.Fatalf("locked offset = %g, want ~-4.8", offset)
			}
		}
	}
	// Persisted: a fresh instance for the same workdir resumes locked.
	c2 := newSSIMU2Calibration(dir)
	offset, locked := c2.Offset()
	if !locked || offset > -4.7 || offset < -4.9 {
		t.Fatalf("resumed calibration = (%g, %v), want (~-4.8, true)", offset, locked)
	}
	// Further samples must not move a locked offset.
	if got, justLocked := c2.AddSample(quality.JODAnchorTarget, quality.SSIMU2FromJOD(quality.JODAnchorTarget)+20); justLocked || got != offset {
		t.Fatalf("locked offset moved: (%g, %v)", got, justLocked)
	}
}
