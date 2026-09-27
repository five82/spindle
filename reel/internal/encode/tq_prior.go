package encode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/five82/reel/internal/quality"
)

const (
	targetQualityNeighborMaxDistance = 8
	// targetQualityDefaultJODPerCRF is the no-information JOD-per-CRF slope
	// used until measured slopes accumulate. Observed slopes across the 1080p
	// and 4K test corpus cluster at 0.02-0.03; the former SDR-only 0.04 halved
	// the cold second-probe step and cost extra probes on high-CRF SDR content
	// (2026-07-01 replay simulation + A/B; see docs/PERFORMANCE_TESTING.md), so
	// one slope now serves every tier.
	targetQualityDefaultJODPerCRF   = 0.025
	targetQualityPriorMaxAdjustment = 3.0
)

// targetQualityPrior seeds each chunk's CRF search from completed chunks:
// nearby chunks' converged CRFs (normalized toward the target) and the
// measured score-per-CRF slope.
type targetQualityPrior struct {
	mu            sync.Mutex
	crfs          map[int]float32
	slopes        []float32
	defaultCRF    float32
	minCRF        float32
	maxCRF        float32
	target        float32
	defaultJODCRF float32
	slopeMin      float32
	slopeMax      float32
}

func newTargetQualityPrior(defaultCRF, minCRF, maxCRF, target, defaultJODPerCRF float32, metric quality.MetricKind) *targetQualityPrior {
	if defaultJODPerCRF <= 0 {
		defaultJODPerCRF = metric.DefaultSlopePerCRF()
	}
	slopeMin, slopeMax := metric.SlopeClamp()
	return &targetQualityPrior{
		crfs:          make(map[int]float32),
		defaultCRF:    clampCRF(defaultCRF, minCRF, maxCRF),
		minCRF:        minCRF,
		maxCRF:        maxCRF,
		target:        target,
		defaultJODCRF: defaultJODPerCRF,
		slopeMin:      slopeMin,
		slopeMax:      slopeMax,
	}
}

// seedTargetQualityPrior replays the per-chunk target logs of already-done
// chunks into the prior so a resumed run starts with the priors it had earned.
func seedTargetQualityPrior(workDir string, doneSet map[int]bool, prior *targetQualityPrior, metric quality.MetricKind, calibration *ssimu2Calibration) {
	for idx := range doneSet {
		path := filepath.Join(workDir, "tq", fmt.Sprintf("%04d.json", idx))
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var log chunkTargetLog
		if err := json.Unmarshal(data, &log); err != nil {
			continue
		}
		probes := log.Probes
		if metric == quality.MetricSSIMU2 && log.Metric != string(quality.MetricSSIMU2) {
			// Warmup (CVVDP) chunk from an interrupted SSIMU2 run: map its
			// JOD scores onto the prior's SSIMU2 scale.
			offset := float32(0)
			if calibration != nil {
				offset = calibration.CurrentOffset()
			}
			probes = make([]quality.Probe, len(log.Probes))
			for i, p := range log.Probes {
				p.Score = quality.SSIMU2FromJOD(p.Score) + offset
				probes[i] = p
			}
		}
		prior.AddResult(idx, log.FinalCRF, probes)
	}
}

func (p *targetQualityPrior) AddResult(chunkIdx int, crf float32, probes []quality.Probe) {
	if crf <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.slopes = append(p.slopes, probeSlopes(probes, p.slopeMin, p.slopeMax)...)
	p.crfs[chunkIdx] = p.normalizedCRF(crf, probes)
}

func (p *targetQualityPrior) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.crfs)
}

// SetTarget shifts the score the prior normalizes against; called once when
// the per-title SSIMU2 calibration locks. Entries recorded earlier were
// normalized against the pre-lock target, which is at most the calibration
// offset away -- inside the +-3 CRF normalization clamp.
func (p *targetQualityPrior) SetTarget(target float32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = target
}

func (p *targetQualityPrior) JODPerCRF() float32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jodPerCRFLocked()
}

func (p *targetQualityPrior) jodPerCRFLocked() float32 {
	if len(p.slopes) == 0 {
		return p.defaultJODCRF
	}
	return medianFloat32(append([]float32(nil), p.slopes...))
}

func (p *targetQualityPrior) normalizedCRF(crf float32, probes []quality.Probe) float32 {
	normalized := crf
	if score, ok := probeScoreAtCRF(probes, crf); ok {
		if slope := p.jodPerCRFLocked(); slope > 0 {
			adjust := (score - p.target) / slope
			if adjust > targetQualityPriorMaxAdjustment {
				adjust = targetQualityPriorMaxAdjustment
			} else if adjust < -targetQualityPriorMaxAdjustment {
				adjust = -targetQualityPriorMaxAdjustment
			}
			normalized = crf + adjust
		}
	}
	return clampCRF(normalized, p.minCRF, p.maxCRF)
}

func probeScoreAtCRF(probes []quality.Probe, crf float32) (float32, bool) {
	want := quality.RoundCRFToQuarter(crf)
	for _, probe := range probes {
		if quality.RoundCRFToQuarter(probe.CRF) == want {
			return probe.Score, true
		}
	}
	return 0, false
}

func (p *targetQualityPrior) InitialCRF(chunkIdx int) (float32, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.crfs) == 0 {
		return p.defaultCRF, "default"
	}

	lowerIdx, upperIdx := 0, 0
	lowerCRF, upperCRF := float32(0), float32(0)
	lowerOK, upperOK := false, false
	values := make([]float32, 0, len(p.crfs))
	for idx, crf := range p.crfs {
		values = append(values, crf)
		switch {
		case idx == chunkIdx:
			return crf, "same"
		case idx < chunkIdx && (!lowerOK || idx > lowerIdx):
			lowerIdx, lowerCRF, lowerOK = idx, crf, true
		case idx > chunkIdx && (!upperOK || idx < upperIdx):
			upperIdx, upperCRF, upperOK = idx, crf, true
		}
	}

	lowerDist, upperDist := targetQualityNeighborMaxDistance+1, targetQualityNeighborMaxDistance+1
	if lowerOK {
		lowerDist = chunkIdx - lowerIdx
	}
	if upperOK {
		upperDist = upperIdx - chunkIdx
	}
	lowerNear := lowerOK && lowerDist <= targetQualityNeighborMaxDistance
	upperNear := upperOK && upperDist <= targetQualityNeighborMaxDistance
	switch {
	case lowerNear && upperNear:
		crf := (lowerCRF*float32(upperDist) + upperCRF*float32(lowerDist)) / float32(lowerDist+upperDist)
		return clampCRF(crf, p.minCRF, p.maxCRF), "neighbor"
	case lowerNear:
		return clampCRF(lowerCRF, p.minCRF, p.maxCRF), "neighbor"
	case upperNear:
		return clampCRF(upperCRF, p.minCRF, p.maxCRF), "neighbor"
	default:
		// Seeding from the nearest completed chunk instead of the median was
		// tested and rejected (2026-07-02): flat on sullyhv, worse on ko.
		// Beyond the neighbor cap a single distant chunk is a noisier
		// estimator than the median. See docs/PERFORMANCE_TESTING.md.
		return medianCRF(values, p.minCRF, p.maxCRF), "median"
	}
}

func medianCRF(values []float32, minCRF, maxCRF float32) float32 {
	if len(values) == 0 {
		return clampCRF(0, minCRF, maxCRF)
	}
	return clampCRF(medianFloat32(values), minCRF, maxCRF)
}

func probeSlopes(probes []quality.Probe, slopeMin, slopeMax float32) []float32 {
	if len(probes) < 2 {
		return nil
	}
	probes = append([]quality.Probe(nil), probes...)
	sort.Slice(probes, func(i, j int) bool { return probes[i].CRF < probes[j].CRF })
	slopes := make([]float32, 0, len(probes)-1)
	for i := 0; i < len(probes)-1; i++ {
		left := probes[i]
		right := probes[i+1]
		crfDelta := right.CRF - left.CRF
		scoreDelta := right.Score - left.Score
		if crfDelta <= 0 || scoreDelta >= 0 {
			continue
		}
		slope := -scoreDelta / crfDelta
		if slope >= slopeMin && slope <= slopeMax {
			slopes = append(slopes, slope)
		}
	}
	return slopes
}

func medianFloat32(values []float32) float32 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func clampCRF(crf, minCRF, maxCRF float32) float32 {
	crf = quality.RoundCRFToQuarter(crf)
	if crf < minCRF {
		return quality.RoundCRFToQuarter(minCRF)
	}
	if crf > maxCRF {
		return quality.RoundCRFToQuarter(maxCRF)
	}
	return crf
}
