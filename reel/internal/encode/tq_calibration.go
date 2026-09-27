package encode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/five82/reel/internal/quality"
)

// ssimu2CalibrationMinSamples anchors the per-title SSIMU2 offset. Samples
// are dual-scored warmup probes (CVVDP + SSIMU2 on the same IVF). Within-title
// mapping spread runs up to ~6 SSIMU2 points sd on clean digital content, so
// a 10-sample median was too noisy in practice (air locked at +3.17 vs a true
// +1.6 and over-encoded +17% in size); 20 samples halve that error (~0.9-1.7
// points, ~0.03 JOD). Titles whose chunks converge quickly dispatch ~20+
// chunks before lock regardless (the warmup population is set by dispatch
// racing ahead of sample collection), so the practical extra cost falls
// mainly on titles that were locking fastest -- exactly the noisy ones.
const ssimu2CalibrationMinSamples = 20

// ssimu2Calibration measures a title's offset from the corpus-level
// SSIMU2<->JOD anchor (quality.SSIMU2FromJOD). Titles differ systematically:
// grainy content scores several SSIMU2 points lower at equal CVVDP quality
// than clean digital content, so a global SSIMU2 target over-encodes grain
// (+32% size on the bts pilot A/B) and under-protects clean outliers. The
// first chunks of a title therefore probe with CVVDP exactly as before,
// each probe also gets a cheap SSIMU2 pass, and once enough (JOD, SSIMU2)
// pairs accumulate the remaining chunks search with pure SSIMU2 against the
// offset-corrected target. The offset persists to the workdir for resume.
type ssimu2Calibration struct {
	mu       sync.Mutex
	path     string
	samples  []float32
	locked   bool
	offset   float32
	claims   int
	lockedCh chan struct{}
}

type ssimu2CalibrationFile struct {
	Offset  float32 `json:"offset"`
	Samples int     `json:"samples"`
}

func newSSIMU2Calibration(workDir string) *ssimu2Calibration {
	c := &ssimu2Calibration{
		path:     filepath.Join(workDir, "tq", "ssimu2-calibration.json"),
		lockedCh: make(chan struct{}),
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	var f ssimu2CalibrationFile
	if json.Unmarshal(data, &f) == nil {
		c.locked = true
		c.offset = f.Offset
		close(c.lockedCh)
	}
	return c
}

// ClaimWarmup reserves a warmup (CVVDP) slot for a chunk. Warmup is bounded
// at ssimu2CalibrationMinSamples chunks: every warmup chunk contributes at
// least one dual-scored probe, so the offset is guaranteed to lock once the
// claimed chunks' first probes complete. Later chunks wait for the lock
// (WaitLocked) instead of joining the warmup -- without the bound, dispatch
// races ahead of sample collection and entire short titles run at CVVDP cost
// (measured: 32/32 chunks on a 5m clip).
func (c *ssimu2Calibration) ClaimWarmup() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locked || c.claims >= ssimu2CalibrationMinSamples {
		return false
	}
	c.claims++
	return true
}

// WaitLocked blocks until the offset locks or the context ends. Callers must
// release their encode slot first: the lock waits on warmup chunks whose
// probes need slots.
func (c *ssimu2Calibration) WaitLocked(ctx context.Context) error {
	select {
	case <-c.lockedCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AddSample records one dual-scored warmup probe and locks the offset once
// enough samples exist. Returns (offset, justLocked) so the caller can log
// the lock exactly once.
func (c *ssimu2Calibration) AddSample(jod, s2 float32) (float32, bool) {
	residual := s2 - quality.SSIMU2FromJOD(jod)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locked {
		return c.offset, false
	}
	c.samples = append(c.samples, residual)
	if len(c.samples) < ssimu2CalibrationMinSamples {
		return 0, false
	}
	c.offset = medianFloat32(append([]float32(nil), c.samples...))
	c.locked = true
	close(c.lockedCh)
	c.persistLocked()
	return c.offset, true
}

// Offset returns the locked per-title offset, or (0, false) while warmup
// samples are still accumulating.
func (c *ssimu2Calibration) Offset() (float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset, c.locked
}

// CurrentOffset is Offset without the locked flag, for score rescaling
// (0 until locked).
func (c *ssimu2Calibration) CurrentOffset() float32 {
	offset, _ := c.Offset()
	return offset
}

func (c *ssimu2Calibration) SampleCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.samples)
}

func (c *ssimu2Calibration) persistLocked() {
	data, err := json.MarshalIndent(ssimu2CalibrationFile{Offset: c.offset, Samples: len(c.samples)}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(c.path, append(data, '\n'), 0644)
}
