package quality

import "testing"

func TestSearchConverges(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.05, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no first CRF")
	}
	state.AddProbe(ctx, Probe{CRF: crf, Score: 9.51})
	if state.StopReason != StopConverged {
		t.Fatalf("stop reason = %q, want converged", state.StopReason)
	}
	best, ok := state.BestProbe(ctx)
	if !ok || best.CRF != crf {
		t.Fatalf("best probe = %+v ok=%v", best, ok)
	}
}

func TestSearchRejectsScoresAboveRange(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.1, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.6062})
	if state.StopReason == StopConverged {
		t.Fatal("score above range should not converge")
	}
	if state.SearchMin != 20.25 {
		t.Fatalf("SearchMin = %g, want 20.25", state.SearchMin)
	}
}

func TestSearchRejectsScoresBelowRange(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.1, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.3938})
	if state.StopReason == StopConverged {
		t.Fatal("score below range should not converge")
	}
	if state.SearchMax != 19.75 {
		t.Fatalf("SearchMax = %g, want 19.75", state.SearchMax)
	}
}

func TestSearchBelowFloorLowersCRF(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.1, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	// Whole-chunk score below the floor (Target-Tolerance=9.4) must not converge
	// and must lower the CRF ceiling.
	state.AddProbe(ctx, Probe{CRF: 24, Score: 9.28})
	if state.StopReason == StopConverged {
		t.Fatal("probe below the quality floor should not converge")
	}
	if state.SearchMax != 23.75 {
		t.Fatalf("SearchMax = %g, want 23.75", state.SearchMax)
	}
}

func TestBestProbePrefersAboveFloor(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.1, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	// CRF 24 is closer to target by raw error but below the floor (9.39 < 9.4);
	// the above-floor probe is preferred even though it is slightly farther.
	state.Probes = []Probe{
		{CRF: 24, Score: 9.39},
		{CRF: 27, Score: 9.65},
	}
	best, ok := state.BestProbe(ctx)
	if !ok || best.CRF != 27 {
		t.Fatalf("best probe = %+v ok=%v, want CRF 27 (above floor)", best, ok)
	}
}

func TestBestProbeFallsBackWhenAllBelowFloor(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.1, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	// Every probe is below the floor; fall back to the one closest to target.
	state.Probes = []Probe{
		{CRF: 20, Score: 9.2},
		{CRF: 24, Score: 9.35},
	}
	best, ok := state.BestProbe(ctx)
	if !ok || best.CRF != 24 {
		t.Fatalf("best probe = %+v ok=%v, want fallback closest score", best, ok)
	}
}

func TestSearchStartsAtInitialCRF(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.05, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 26}
	state := NewSearchState(ctx)
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no first CRF")
	}
	if crf != 26 {
		t.Fatalf("first CRF = %g, want 26", crf)
	}
}

func TestSearchSecondProbeUsesEstimatedStep(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.05, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 26}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 26, Score: 9.3})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no second CRF")
	}
	if crf != 21 {
		t.Fatalf("second CRF = %g, want 21", crf)
	}
}

func TestSearchSecondProbeUsesLargerStepForLargeMiss(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.05, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 26, JODPerCRF: 0.04}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 26, Score: 9.1})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no second CRF")
	}
	if crf != 16 {
		t.Fatalf("second CRF = %g, want 16", crf)
	}
}

func TestSearchSecondProbeCapsExtremeMiss(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.05, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 26, JODPerCRF: 0.02}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 26, Score: 10.1})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no second CRF")
	}
	if crf != 56 {
		t.Fatalf("second CRF = %g, want 56", crf)
	}
}

func TestSearchUsesMidpointForUnbracketedLowProbes(t *testing.T) {
	ctx := SearchContext{Target: 9.375, Tolerance: 0.125, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 39.75, JODPerCRF: 0.04}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 39.75, Score: 9.1704})
	state.AddProbe(ctx, Probe{CRF: 34.5, Score: 9.2008})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no third CRF")
	}
	if crf != 19.25 {
		t.Fatalf("third CRF = %g, want bounded midpoint 19.25", crf)
	}
}

func TestSearchAcceleratesFlatUnbracketedHighProbes(t *testing.T) {
	ctx := SearchContext{Target: 9.375, Tolerance: 0.125, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 27, JODPerCRF: 0.025}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 27, Score: 9.9255})
	state.AddProbe(ctx, Probe{CRF: 41.5, Score: 9.8751})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no third CRF")
	}
	if crf != 58.25 {
		t.Fatalf("third CRF = %g, want aggressive high-side probe 58.25", crf)
	}
}

func TestSearchUsesMidpointForMildFlatUnbracketedHighProbes(t *testing.T) {
	ctx := SearchContext{Target: 9.375, Tolerance: 0.125, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6, InitialCRF: 24.75, JODPerCRF: 0.04}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 24.75, Score: 9.677})
	state.AddProbe(ctx, Probe{CRF: 28.75, Score: 9.563})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no third CRF")
	}
	if crf != 46.5 {
		t.Fatalf("third CRF = %g, want bounded midpoint 46.5", crf)
	}
}

func TestSearchInterpolatesBracketedProbes(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.01, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.8})
	state.AddProbe(ctx, Probe{CRF: 30, Score: 9.2})
	crf, ok := state.NextCRF(ctx)
	if !ok {
		t.Fatal("no third CRF")
	}
	if crf != 25 {
		t.Fatalf("third CRF = %g, want interpolated 25", crf)
	}
}

func TestSearchBoundsUpdateForCVVDP(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.05, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 34, Score: 9.0})
	if state.SearchMax != 33.75 {
		t.Fatalf("low score SearchMax = %g, want 33.75", state.SearchMax)
	}
	state = NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 34, Score: 9.9})
	if state.SearchMin != 34.25 {
		t.Fatalf("high score SearchMin = %g, want 34.25", state.SearchMin)
	}
}

func TestSearchMonotonicityGuard(t *testing.T) {
	ctx := SearchContext{Target: 9.5, Tolerance: 0.01, CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6}
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.8})
	state.AddProbe(ctx, Probe{CRF: 30, Score: 9.9})
	if state.StopReason != StopMonotonicity {
		t.Fatalf("stop reason = %q, want monotonicity", state.StopReason)
	}
}

func TestInterpolateCRF(t *testing.T) {
	probes := []Probe{{CRF: 20, Score: 9.8}, {CRF: 30, Score: 9.2}}
	got := InterpolateCRF(probes, 9.5)
	if got != 25 {
		t.Fatalf("InterpolateCRF = %g, want 25", got)
	}
}

func TestInterpolateCRFUsesBracketingSegment(t *testing.T) {
	// Target 9.375 lies between the 9.3 and 9.7 probes; interpolation must use
	// that segment, not the lowest-score pair.
	probes := []Probe{
		{CRF: 38, Score: 9.1},
		{CRF: 33, Score: 9.3},
		{CRF: 25, Score: 9.7},
	}
	got := InterpolateCRF(probes, 9.375)
	// t = (9.375-9.3)/(9.7-9.3) = 0.1875; crf = 33 + 0.1875*(25-33) = 31.5
	if got != 31.5 {
		t.Fatalf("InterpolateCRF = %g, want 31.5", got)
	}
}

func TestInterpolateCRFExtrapolatesNearestSegment(t *testing.T) {
	// Target below all scores extrapolates the lowest segment, matching the
	// prior two-probe linear behavior.
	probes := []Probe{{CRF: 30, Score: 9.4}, {CRF: 20, Score: 9.6}}
	got := InterpolateCRF(probes, 9.3)
	// t = (9.3-9.4)/(9.6-9.4) = -0.5; crf = 30 + (-0.5)*(20-30) = 35
	if got != 35 {
		t.Fatalf("InterpolateCRF = %g, want 35", got)
	}
}

func TestInterpolateCRFFourProbesUsesLocalSegment(t *testing.T) {
	probes := []Probe{
		{CRF: 40, Score: 9.0},
		{CRF: 35, Score: 9.2},
		{CRF: 30, Score: 9.45},
		{CRF: 20, Score: 9.8},
	}
	got := InterpolateCRF(probes, 9.375)
	// Bracketing segment is (9.2, 35) -> (9.45, 30): t = 0.7; crf = 35 - 3.5 = 31.5
	if got != 31.5 {
		t.Fatalf("InterpolateCRF = %g, want 31.5", got)
	}
}

// Rate-aware search: probes whose encodes escaped the bitstream cap are
// steering-only ("go higher"), never winners. Sizes below use FPS=24,
// Frames=240 (10s), MaxRateBps=40e6: over-rate above 52.5 MB (42 Mbps with
// the 5% slack).

func rateCtx() SearchContext {
	return SearchContext{
		Target: 9.35, Tolerance: 0.15,
		CRFMin: 4.25, CRFMax: 63.75, MaxProbes: 6,
		MaxRateBps: 40e6, FPS: 24,
	}
}

func TestSearchOverRateProbeCannotConverge(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	// In-tolerance score but 48 Mbps: must not converge, must push CRF up.
	state.AddProbe(ctx, Probe{CRF: 10, Score: 9.35, Size: 60e6, Frames: 240})
	if state.StopReason == StopConverged {
		t.Fatal("over-rate probe must not converge the search")
	}
	if state.SearchMin != 10.25 {
		t.Fatalf("SearchMin = %g, want 10.25", state.SearchMin)
	}
}

func TestSearchBestProbeSkipsOverRateProbes(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 25, Score: 8.7, Size: 30e6, Frames: 240})
	// Closer to target but over-rate (the chunk-0378 failure shape).
	state.AddProbe(ctx, Probe{CRF: 8.5, Score: 9.3, Size: 65e6, Frames: 240})
	best, ok := state.BestProbe(ctx)
	if !ok || best.CRF != 25 {
		t.Fatalf("best probe CRF = %g ok=%v, want in-rate probe at 25", best.CRF, ok)
	}
}

func TestSearchMonotonicityIgnoresOverRateProbes(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	// Over-rate probe with a regulator-distorted (high) score.
	state.AddProbe(ctx, Probe{CRF: 13, Score: 9.3, Size: 60e6, Frames: 240})
	// In-rate probe at higher CRF with lower score would trip the guard if
	// compared against the distorted probe.
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.0, Size: 30e6, Frames: 240})
	if state.StopReason == StopMonotonicity {
		t.Fatal("monotonicity guard must ignore over-rate probes")
	}
}

func TestSearchAllOverRateFallsBackToHighestCRF(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 10, Score: 9.4, Size: 70e6, Frames: 240})
	state.AddProbe(ctx, Probe{CRF: 14, Score: 9.2, Size: 60e6, Frames: 240})
	best, ok := state.BestProbe(ctx)
	if !ok || best.CRF != 14 {
		t.Fatalf("best probe CRF = %g ok=%v, want least-violating probe at 14", best.CRF, ok)
	}
}

func TestSearchPeakRateGatesProbe(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	// Average well under the cap (24 Mbps) but a 50 Mbps worst second: the
	// probe must be treated as over-rate.
	state.AddProbe(ctx, Probe{CRF: 18, Score: 9.35, Size: 30e6, Frames: 240, PeakBps: 50e6})
	if state.StopReason == StopConverged {
		t.Fatal("peak-violating probe must not converge the search")
	}
	if state.SearchMin != 18.25 {
		t.Fatalf("SearchMin = %g, want 18.25", state.SearchMin)
	}
	// A peak just at the cap passes the gate.
	state.AddProbe(ctx, Probe{CRF: 22, Score: 9.35, Size: 30e6, Frames: 240, PeakBps: 41e6})
	if state.StopReason != StopConverged {
		t.Fatalf("stop reason = %q, want converged for peak-legal probe", state.StopReason)
	}
}

func TestSearchAllOverRateStepsPastCapFrontier(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	// Item-15 chunk 0372: first probe blows the peak gate. Its score must not
	// drive a 0.25 step, and the search must not bisect toward CRFMax (the
	// old path probed CRF 42.75 and scored 8.11).
	state.AddProbe(ctx, Probe{CRF: 21.25, Score: 8.7281, Size: 29.5e6, Frames: 233, PeakBps: 50e6})
	crf, ok := state.NextCRF(ctx)
	if !ok || crf != 23.25 {
		t.Fatalf("next CRF = %g ok=%v, want 23.25 (over-rate CRF + cap frontier)", crf, ok)
	}
	state.AddProbe(ctx, Probe{CRF: crf, Score: 8.73, Size: 25e6, Frames: 233, PeakBps: 46e6})
	if crf, ok = state.NextCRF(ctx); !ok || crf != 25.25 {
		t.Fatalf("next CRF = %g ok=%v, want 25.25 after a second over-rate probe", crf, ok)
	}
}

func TestSearchStopsRateCappedAtFrontier(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 27, Score: 8.78, Size: 18e6, Frames: 233, PeakBps: 50e6})
	// Rate-legal, below the band, within capFrontierCRF of the rejected probe:
	// nothing untried can reach the band, so stop and keep this probe.
	state.AddProbe(ctx, Probe{CRF: 29, Score: 8.76, Size: 15e6, Frames: 233, PeakBps: 30e6})
	if state.StopReason != StopRateCapped {
		t.Fatalf("stop reason = %q, want rate_capped", state.StopReason)
	}
	best, ok := state.BestProbe(ctx)
	if !ok || best.CRF != 29 {
		t.Fatalf("best probe CRF = %g ok=%v, want the rate-legal probe at 29", best.CRF, ok)
	}
}

func TestSearchBisectsWideCapFrontierThenStops(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	// Item-15 chunk 0181 shape: legal first probe just under the band, then a
	// score-driven step down lands over-rate 7.5 CRF below. The frontier is
	// wide, so keep bisecting it until it is resolved within capFrontierCRF.
	state.AddProbe(ctx, Probe{CRF: 24.5, Score: 9.12, Size: 15e6, Frames: 87, PeakBps: 38e6})
	state.AddProbe(ctx, Probe{CRF: 17, Score: 8.84, Size: 12.6e6, Frames: 87, PeakBps: 60e6})
	if state.StopReason != StopNone {
		t.Fatalf("stop reason = %q, want none while the frontier is wide", state.StopReason)
	}
	crf, ok := state.NextCRF(ctx)
	if !ok || crf != 20.75 {
		t.Fatalf("next CRF = %g ok=%v, want frontier midpoint 20.75", crf, ok)
	}
	state.AddProbe(ctx, Probe{CRF: crf, Score: 9.0, Size: 15.8e6, Frames: 87, PeakBps: 55e6})
	crf, ok = state.NextCRF(ctx)
	if !ok || crf != 22.75 {
		t.Fatalf("next CRF = %g ok=%v, want 22.75", crf, ok)
	}
	state.AddProbe(ctx, Probe{CRF: crf, Score: 9.1, Size: 16.5e6, Frames: 87, PeakBps: 50e6})
	if state.StopReason != StopRateCapped {
		t.Fatalf("stop reason = %q, want rate_capped once the frontier is within %g CRF", state.StopReason, float32(capFrontierCRF))
	}
	if best, ok := state.BestProbe(ctx); !ok || best.CRF != 24.5 {
		t.Fatalf("best probe CRF = %g ok=%v, want 24.5", best.CRF, ok)
	}
}

func TestSearchCapHistoryRelabelsBudgetStops(t *testing.T) {
	ctx := rateCtx()
	ctx.MaxProbes = 2
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.0, Size: 30e6, Frames: 240, PeakBps: 50e6})
	state.AddProbe(ctx, Probe{CRF: 30, Score: 8.9, Size: 20e6, Frames: 240, PeakBps: 30e6})
	if state.StopReason != StopRateCapped {
		t.Fatalf("stop reason = %q, want rate_capped: the cap bounded a search that ran out of probes", state.StopReason)
	}
}

func TestSearchConvergesDespiteCapHistory(t *testing.T) {
	ctx := rateCtx()
	state := NewSearchState(ctx)
	state.AddProbe(ctx, Probe{CRF: 20, Score: 9.6, Size: 30e6, Frames: 240, PeakBps: 50e6})
	state.AddProbe(ctx, Probe{CRF: 22, Score: 9.3, Size: 25e6, Frames: 240, PeakBps: 40e6})
	if state.StopReason != StopConverged {
		t.Fatalf("stop reason = %q, want converged", state.StopReason)
	}
}
