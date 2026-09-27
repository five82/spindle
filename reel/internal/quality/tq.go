package quality

import (
	"math"
	"sort"
)

type StopReason string

const (
	StopNone          StopReason = ""
	StopConverged     StopReason = "converged"
	StopBoundsCrossed StopReason = "bounds_crossed"
	StopMonotonicity  StopReason = "monotonicity_guard"
	StopMaxProbes     StopReason = "max_probes"
	StopNoCandidates  StopReason = "no_candidates"
	// StopRateCapped: the bitstream cap bounded the search from below and no
	// rate-legal probe reached the band. Intended behavior on chunks where
	// the cap binds (heavy grain), not a search failure; see capFrontierCRF.
	StopRateCapped StopReason = "rate_capped"
)

// capFrontierCRF is how finely the search resolves the rate-cap frontier: the
// boundary between over-rate and rate-legal CRFs. On a cap-bound chunk the
// regulator holds the rate whatever the CRF, so scores are flat across the
// frontier and resolving it below ~2 CRF (about 0.08 JOD at a typical
// 0.04 JOD/CRF slope, under half the band width) buys nothing. Item-15
// Groundhog Day measurements: 32 of 36 misses burned all six probes here,
// including a wasted midpoint probe near CRF 43 scoring 8.0-8.7.
const capFrontierCRF = 2

// Probe records one target-quality probe encode and its whole-chunk metric
// result. Every probe encodes and scores the entire chunk, so Score is exact
// for the selected metric and the probe IVF can be reused verbatim as the final
// chunk. The old sampled worst-window proxy was removed because it was
// systematically pessimistic and over-encoded; see docs/PERFORMANCE_TESTING.md.
type Probe struct {
	CRF           float32 `json:"crf"`
	Score         float32 `json:"score"`
	Size          uint64  `json:"size"`
	PeakBps       float64 `json:"peak_bps,omitempty"`
	EncodeSeconds float64 `json:"encode_seconds,omitempty"`
	MetricSeconds float64 `json:"metric_seconds,omitempty"`
	Frames        int     `json:"frames,omitempty"`
}

// SearchContext configures per-chunk target-quality search. Target,
// Tolerance, and JODPerCRF are denominated in the probe metric's units
// (CVVDP JOD or SSIMU2 points; Metric's ScoreScale converts the calibrated
// JOD constants).
type SearchContext struct {
	Metric     MetricKind // zero value means CVVDP
	Target     float32
	Tolerance  float32
	CRFMin     float32
	CRFMax     float32
	MaxProbes  int
	InitialCRF float32
	JODPerCRF  float32 // score units per CRF step
	MaxRateBps float64 // bitstream cap; probes exceeding it cannot be selected (0 disables)
	FPS        float64 // frames per second, used to compute probe bitrate
}

// OverRate reports whether a probe's bitrate exceeds the cap, on chunk
// average or on its worst one-second window. Encodes run with the cap active,
// so an over-rate probe means the rate regulator failed to hold at that CRF:
// the probe is unusable regardless of score, and its score is distorted by
// regulator thrash. Decoders chew seconds, not chunk averages, hence the peak
// gate; observed stutter on hardware provisioned for the signaled level came
// from single-second spikes inside chunks whose averages honored the cap. The
// slack keeps legitimately regulated probes that land at the cap from being
// rejected as noise, with the peak gate slightly looser for single-second
// granularity.
func OverRate(ctx SearchContext, p Probe) bool {
	if ctx.MaxRateBps <= 0 {
		return false
	}
	if p.PeakBps > ctx.MaxRateBps*1.10 {
		return true
	}
	if ctx.FPS <= 0 || p.Frames <= 0 {
		return false
	}
	return float64(p.Size)*8*ctx.FPS/float64(p.Frames) > ctx.MaxRateBps*1.05
}

// SearchState tracks target-quality search for one chunk.
type SearchState struct {
	Probes     []Probe    `json:"probes"`
	SearchMin  float32    `json:"search_min"`
	SearchMax  float32    `json:"search_max"`
	Round      int        `json:"round"`
	StopReason StopReason `json:"stop_reason,omitempty"`
	tried      map[int]bool
	capCRF     float32 // highest over-rate CRF seen; 0 until the cap has rejected a probe
}

func NewSearchState(ctx SearchContext) *SearchState {
	return &SearchState{
		SearchMin: RoundCRFToQuarter(ctx.CRFMin),
		SearchMax: RoundCRFToQuarter(ctx.CRFMax),
		tried:     make(map[int]bool),
	}
}

func (s *SearchState) NextCRF(ctx SearchContext) (float32, bool) {
	if s.StopReason != StopNone {
		return 0, false
	}
	if ctx.MaxProbes > 0 && len(s.Probes) >= ctx.MaxProbes {
		s.halt(StopMaxProbes)
		return 0, false
	}
	if s.SearchMin > s.SearchMax {
		s.halt(StopBoundsCrossed)
		return 0, false
	}

	var candidate float32
	switch {
	case len(s.Probes) == 0:
		candidate = initialSearchCRF(ctx)
	case s.capCRF > 0 && len(usableProbes(ctx, s.Probes)) == 0:
		// Every probe so far blew the cap, so no score can steer. Step just
		// past the frontier instead of bisecting toward CRFMax: the frontier
		// sits a few CRF above the rejected probe, and a midpoint probe near
		// CRF 43 only ever scored far below the band.
		candidate = s.capCRF + capFrontierCRF
	case len(s.Probes) == 1:
		candidate = secondSearchCRF(ctx, s.Probes[0])
	default:
		candidate = nextCRFWithHistory(ctx, s)
	}

	candidate, ok := s.firstUntriedInBounds(candidate)
	if !ok {
		s.halt(StopNoCandidates)
		return 0, false
	}
	s.Round++
	return candidate, true
}

func (s *SearchState) AddProbe(ctx SearchContext, probe Probe) {
	probe.CRF = RoundCRFToQuarter(probe.CRF)
	for _, old := range s.Probes {
		if old.CRF == probe.CRF {
			return
		}
	}

	if OverRate(ctx, probe) {
		// The only usable lesson from a cap-violating probe is "search
		// higher CRFs"; its score must not converge the search or feed the
		// monotonicity guard.
		s.Probes = append(s.Probes, probe)
		s.tried[crfKey(probe.CRF)] = true
		s.capCRF = max(s.capCRF, probe.CRF)
		s.SearchMin = max(s.SearchMin, RoundCRFToQuarter(probe.CRF+0.25))
		switch {
		case s.capBound(ctx):
			s.StopReason = StopRateCapped
		case s.SearchMin > s.SearchMax:
			s.halt(StopBoundsCrossed)
		case ctx.MaxProbes > 0 && len(s.Probes) >= ctx.MaxProbes:
			s.halt(StopMaxProbes)
		}
		return
	}

	for _, old := range s.Probes {
		if OverRate(ctx, old) {
			continue
		}
		if (old.CRF-probe.CRF)*(old.Score-probe.Score) >= 0 {
			s.Probes = append(s.Probes, probe)
			s.tried[crfKey(probe.CRF)] = true
			s.halt(StopMonotonicity)
			return
		}
	}

	s.Probes = append(s.Probes, probe)
	s.tried[crfKey(probe.CRF)] = true

	if targetQualityConverged(ctx, probe.Score) {
		s.StopReason = StopConverged
		return
	}
	if probe.Score < ctx.Target-ctx.Tolerance {
		// Quality too low: lower CRF.
		s.SearchMax = RoundCRFToQuarter(probe.CRF - 0.25)
	} else if probe.Score > ctx.Target+ctx.Tolerance {
		// Quality higher than needed: raise CRF.
		s.SearchMin = RoundCRFToQuarter(probe.CRF + 0.25)
	}
	switch {
	case s.capBound(ctx):
		s.StopReason = StopRateCapped
	case s.SearchMin > s.SearchMax:
		s.halt(StopBoundsCrossed)
	case ctx.MaxProbes > 0 && len(s.Probes) >= ctx.MaxProbes:
		s.halt(StopMaxProbes)
	}
}

// halt records a non-converged stop. Once the cap has rejected a probe the
// search was bounded from below by the cap, so the honest reason for any
// later miss is the cap, not the probe budget or the guard.
func (s *SearchState) halt(reason StopReason) {
	if s.capCRF > 0 {
		reason = StopRateCapped
	}
	s.StopReason = reason
}

// capBound reports whether the cap frontier is resolved: the lowest
// rate-legal probe still scores below the band and sits within
// capFrontierCRF above the highest over-rate probe, so no untried CRF can
// gain enough to reach the band.
func (s *SearchState) capBound(ctx SearchContext) bool {
	if s.capCRF == 0 {
		return false
	}
	legal := float32(0)
	found := false
	for _, p := range usableProbes(ctx, s.Probes) {
		if p.Score < ctx.Target-ctx.Tolerance && (!found || p.CRF < legal) {
			legal = p.CRF
			found = true
		}
	}
	return found && legal > s.capCRF && legal-s.capCRF <= capFrontierCRF
}

// usableProbes drops over-rate probes: their scores are regulator-distorted
// and must not steer score-based decisions. (Their CRFs still shaped the
// search bounds when they were added.)
func usableProbes(ctx SearchContext, probes []Probe) []Probe {
	usable := make([]Probe, 0, len(probes))
	for _, probe := range probes {
		if !OverRate(ctx, probe) {
			usable = append(usable, probe)
		}
	}
	return usable
}

func (s *SearchState) BestProbe(ctx SearchContext) (Probe, bool) {
	if len(s.Probes) == 0 {
		return Probe{}, false
	}
	best, found := bestProbeMatching(ctx, s.Probes, func(probe Probe) bool {
		return !OverRate(ctx, probe) && probe.Score >= ctx.Target-ctx.Tolerance
	})
	if found {
		return best, true
	}
	if best, found = bestProbeMatching(ctx, s.Probes, func(probe Probe) bool {
		return !OverRate(ctx, probe)
	}); found {
		return best, true
	}
	// Every probe blew the cap; the highest CRF is the least-violating one.
	best = s.Probes[0]
	for _, probe := range s.Probes[1:] {
		if probe.CRF > best.CRF {
			best = probe
		}
	}
	return best, true
}

func bestProbeMatching(ctx SearchContext, probes []Probe, keep func(Probe) bool) (Probe, bool) {
	const errEpsilon = 0.001
	var best Probe
	bestErr := float64(0)
	found := false
	for _, probe := range probes {
		if !keep(probe) {
			continue
		}
		err := math.Abs(float64(probe.Score - ctx.Target))
		if !found || err < bestErr-errEpsilon || (math.Abs(err-bestErr) <= errEpsilon && probe.Score > best.Score) {
			best = probe
			bestErr = err
			found = true
		}
	}
	return best, found
}

func targetQualityConverged(ctx SearchContext, score float32) bool {
	return score >= ctx.Target-ctx.Tolerance && score <= ctx.Target+ctx.Tolerance
}

func initialSearchCRF(ctx SearchContext) float32 {
	if ctx.InitialCRF > 0 {
		return RoundCRFToQuarter(ctx.InitialCRF)
	}
	return RoundCRFToQuarter((ctx.CRFMin + ctx.CRFMax) / 2)
}

func nextCRFWithHistory(ctx SearchContext, state *SearchState) float32 {
	usable := usableProbes(ctx, state.Probes)
	if probesBracketTarget(ctx, usable) {
		candidate := InterpolateCRF(usable, ctx.Target)
		if candidate >= state.SearchMin && candidate <= state.SearchMax {
			return candidate
		}
	}
	if probesAreAllAboveTarget(ctx, usable) {
		return unbracketedHighCRF(ctx, state, usable)
	}
	return RoundCRFToQuarter((state.SearchMin + state.SearchMax) / 2)
}

func probesBracketTarget(ctx SearchContext, probes []Probe) bool {
	below := false
	above := false
	for _, probe := range probes {
		side := probeTargetSide(ctx, probe)
		below = below || side < 0
		above = above || side > 0
	}
	return below && above
}

func probesAreAllAboveTarget(ctx SearchContext, probes []Probe) bool {
	if len(probes) == 0 {
		return false
	}
	for _, probe := range probes {
		if probeTargetSide(ctx, probe) <= 0 {
			return false
		}
	}
	return true
}

func probeTargetSide(ctx SearchContext, probe Probe) int {
	if probe.Score < ctx.Target {
		return -1
	}
	if probe.Score > ctx.Target {
		return 1
	}
	return 0
}

func unbracketedHighCRF(ctx SearchContext, state *SearchState, usable []Probe) float32 {
	candidate := (state.SearchMin + state.SearchMax) / 2
	if shouldUseAggressiveHighCRFJump(ctx, usable) {
		candidate = state.SearchMin + (state.SearchMax-state.SearchMin)*0.75
	}
	return RoundCRFToQuarter(candidate)
}

func shouldUseAggressiveHighCRFJump(ctx SearchContext, probes []Probe) bool {
	// 0.30 JOD in the probe metric's units.
	minHighSideDelta := 0.30 * ctx.Metric.ScoreScale()
	score, ok := highestCRFScore(probes)
	if !ok || score-ctx.Target < minHighSideDelta {
		return false
	}
	slope, ok := highestCRFSlope(probes)
	return ok && slope < searchJODPerCRF(ctx)*0.5
}

func highestCRFScore(probes []Probe) (float32, bool) {
	if len(probes) == 0 {
		return 0, false
	}
	best := probes[0]
	for _, probe := range probes[1:] {
		if probe.CRF > best.CRF {
			best = probe
		}
	}
	return best.Score, true
}

func highestCRFSlope(probes []Probe) (float32, bool) {
	if len(probes) < 2 {
		return 0, false
	}
	probes = append([]Probe(nil), probes...)
	sort.Slice(probes, func(i, j int) bool { return probes[i].CRF < probes[j].CRF })
	for i := len(probes) - 1; i > 0; i-- {
		crfDelta := probes[i].CRF - probes[i-1].CRF
		scoreDrop := probes[i-1].Score - probes[i].Score
		if crfDelta > 0 && scoreDrop > 0 {
			return scoreDrop / crfDelta, true
		}
	}
	return 0, false
}

func searchJODPerCRF(ctx SearchContext) float32 {
	if ctx.JODPerCRF > 0 {
		return ctx.JODPerCRF
	}
	// Fallback for a missing slope: 0.04 JOD/CRF in the probe metric's
	// units. Production always populates JODPerCRF.
	return 0.04 * ctx.Metric.ScoreScale()
}

func secondSearchCRF(ctx SearchContext, probe Probe) float32 {
	jodPerCRF := searchJODPerCRF(ctx)
	delta := probe.Score - ctx.Target
	step := float32(math.Abs(float64(delta / jodPerCRF)))
	if maxStep := secondSearchMaxStep(ctx, delta); step > maxStep {
		step = maxStep
	}
	if delta > 0 {
		// Quality is higher than needed; raise CRF.
		return RoundCRFToQuarter(probe.CRF + step)
	}
	// Quality is too low; lower CRF.
	return RoundCRFToQuarter(probe.CRF - step)
}

func secondSearchMaxStep(ctx SearchContext, delta float32) float32 {
	// Thresholds are 0.40 / 0.25 JOD in the probe metric's units.
	scale := ctx.Metric.ScoreScale()
	absDelta := float32(math.Abs(float64(delta)))
	switch {
	case absDelta >= 0.40*scale:
		return 30
	case absDelta >= 0.25*scale:
		return 20
	default:
		return 10
	}
}

func (s *SearchState) firstUntriedInBounds(candidate float32) (float32, bool) {
	candidate = RoundCRFToQuarter(candidate)
	if candidate < s.SearchMin {
		candidate = s.SearchMin
	}
	if candidate > s.SearchMax {
		candidate = s.SearchMax
	}
	if !s.tried[crfKey(candidate)] {
		return candidate, true
	}

	for step := float32(0.25); s.SearchMin <= candidate-step || candidate+step <= s.SearchMax; step += 0.25 {
		lo := RoundCRFToQuarter(candidate - step)
		if lo >= s.SearchMin && !s.tried[crfKey(lo)] {
			return lo, true
		}
		hi := RoundCRFToQuarter(candidate + step)
		if hi <= s.SearchMax && !s.tried[crfKey(hi)] {
			return hi, true
		}
	}
	return 0, false
}

func crfKey(crf float32) int {
	return int(math.Round(float64(crf * 4)))
}

// InterpolateCRF linearly interpolates a CRF for the target score using the
// pair of adjacent probes (ordered by score) whose scores bracket the target.
// If the target falls outside all probe scores, the nearest segment
// extrapolates, matching the prior linear behavior at two probes.
func InterpolateCRF(probes []Probe, target float32) float32 {
	if len(probes) == 0 {
		return 0
	}
	if len(probes) == 1 {
		return RoundCRFToQuarter(probes[0].CRF)
	}
	sorted := append([]Probe(nil), probes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Score < sorted[j].Score })
	k := len(sorted) - 2
	for i := 0; i < len(sorted)-1; i++ {
		if target <= sorted[i+1].Score {
			k = i
			break
		}
	}
	lo, hi := sorted[k], sorted[k+1]
	if hi.Score == lo.Score {
		return RoundCRFToQuarter((lo.CRF + hi.CRF) / 2)
	}
	t := (target - lo.Score) / (hi.Score - lo.Score)
	return RoundCRFToQuarter(lo.CRF + t*(hi.CRF-lo.CRF))
}
