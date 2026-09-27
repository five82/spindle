package encode

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/encoder"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/video"
)

func TestSelectGrainSampleChunksSpacesAcrossTheMiddle(t *testing.T) {
	// 100 chunks of 120 frames: 12000 frames, middle 60% is 2400-9600.
	chunks := makeChunks(100, 120)
	samples := selectGrainSampleChunks(chunks, 12000)

	if len(samples) != grainGateSamples {
		t.Fatalf("samples = %d, want %d", len(samples), grainGateSamples)
	}
	for _, ch := range samples {
		if ch.Start < 2400 || ch.End > 9600 {
			t.Errorf("chunk %04d (%d-%d) is outside the middle 60%%", ch.Idx, ch.Start, ch.End)
		}
	}
	for i := 1; i < len(samples); i++ {
		if samples[i].Idx <= samples[i-1].Idx {
			t.Fatalf("samples are not in increasing order: %v", sampleIndices(samples))
		}
	}
	// Evenly spaced means the first and last eligible chunks are the endpoints.
	if samples[0].Idx != 20 || samples[len(samples)-1].Idx != 79 {
		t.Errorf("endpoints = %d..%d, want 20..79", samples[0].Idx, samples[len(samples)-1].Idx)
	}
}

func TestSelectGrainSampleChunksShortTitles(t *testing.T) {
	// Two long chunks, neither inside the middle 60%: the fallback still
	// measures them rather than skipping the gate.
	chunks := []chunk.Chunk{
		{Idx: 0, Start: 0, End: 200},
		{Idx: 1, Start: 200, End: 400},
	}
	samples := selectGrainSampleChunks(chunks, 400)
	if len(samples) != 2 {
		t.Fatalf("samples = %v, want both chunks", sampleIndices(samples))
	}

	// One eligible chunk takes the middle of the eligible list, not an endpoint.
	one := selectGrainSampleChunks([]chunk.Chunk{{Idx: 7, Start: 0, End: 150}}, 150)
	if len(one) != 1 || one[0].Idx != 7 {
		t.Fatalf("single-chunk selection = %v", sampleIndices(one))
	}

	// Nothing long enough to measure: no samples, so the title goes untreated.
	short := selectGrainSampleChunks(makeChunks(40, grainGateMinChunkFrames-1), 40*(grainGateMinChunkFrames-1))
	if len(short) != 0 {
		t.Fatalf("short chunks should not be sampled, got %v", sampleIndices(short))
	}
	if len(selectGrainSampleChunks(nil, 0)) != 0 {
		t.Error("no chunks should produce no samples")
	}
}

func TestGrainTreatmentThresholds(t *testing.T) {
	for _, tc := range []struct {
		width            uint32
		ambiguous, treat float64
	}{{3840, uhdAmbiguousBPP, uhdLightBPP}, {1920, hdAmbiguousBPP, hdLightBPP}, {1280, 0, 0}} {
		ambiguous, treat := bppCutoffs(tc.width)
		if ambiguous != tc.ambiguous || treat != tc.treat {
			t.Errorf("%d: cutoffs %v/%v, want %v/%v", tc.width, ambiguous, treat, tc.ambiguous, tc.treat)
		}
	}
	for _, tc := range []struct {
		bpp  float64
		want bool
	}{
		{0.30, true}, {uhdLightBPP, true}, {uhdLightBPP - 1e-6, false}, {0.01, false},
	} {
		if got := grainTreats(tc.bpp, uhdLightBPP); got != tc.want {
			t.Errorf("treat(%.6f) = %v, want %v", tc.bpp, got, tc.want)
		}
	}
	if grainTreats(10, 0) {
		t.Error("SD must not be treated")
	}
}

// TestGrainCutoffsMatchTheirMbpsLandmarks pins the constants to the bitrates
// their comments quote, which is how the study reported them.
func TestGrainCutoffsMatchTheirMbpsLandmarks(t *testing.T) {
	inf := &video.Info{FPSNum: 24000, FPSDen: 1001}
	for _, tc := range []struct {
		bpp  float64
		mbps float64
	}{
		{uhdLightBPP, 14},
	} {
		got := mbpsFromBPP(tc.bpp, 3840, 2160, inf)
		if math.Abs(got-tc.mbps) > 0.2 {
			t.Errorf("%.4f bpp = %.2f Mbps at 3840x2160@23.976, want %.0f", tc.bpp, got, tc.mbps)
		}
	}
}

func TestMedian(t *testing.T) {
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("odd median = %v, want 2", got)
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("even median = %v, want 2.5", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("empty median = %v, want 0", got)
	}
}

// Exercise the actual SVT parser/encoder with fitted AR and chroma parameters,
// not just a hand-written white-noise fixture.
func TestEstimatedGrainTableEncodes(t *testing.T) {
	if !encoder.FGSTableSupported() {
		t.Skip("linked SVT-AV1 does not support grain tables")
	}
	estimate := testGrainEstimate(t)
	path := filepath.Join(t.TempDir(), "estimated.tbl")
	if err := writeGrainFile(path, []byte(estimate.Table)); err != nil {
		t.Fatal(err)
	}
	if err := encodeWithGrainTable(path); err != nil {
		t.Fatalf("encode with estimated grain: %v", err)
	}

	// Control: a table the reader rejects must fail, otherwise the tests above
	// would pass even if the table were silently ignored.
	bogus := filepath.Join(t.TempDir(), "bogus.tbl")
	if err := os.WriteFile(bogus, []byte("not a grain table\n"), 0644); err != nil {
		t.Fatalf("write bogus table: %v", err)
	}
	if err := encodeWithGrainTable(bogus); err == nil {
		t.Error("a malformed grain table should fail the encode")
	}
}

func encodeWithGrainTable(tablePath string) error {
	out := filepath.Join(os.TempDir(), "reel-grain-table-test.ivf")
	defer func() { _ = os.Remove(out) }()

	const width, height = 64, 64
	cfg := &encoder.EncConfig{
		Inf:        &video.Info{FPSNum: 24, FPSDen: 1},
		CRF:        40,
		Preset:     12,
		Output:     out,
		GrainTable: &tablePath,
		Width:      width,
		Height:     height,
		Frames:     2,
	}
	rng := rand.New(rand.NewSource(1))
	readFrame := func(buf []byte) error {
		for i := 0; i+1 < len(buf); i += 2 {
			binary.LittleEndian.PutUint16(buf[i:], uint16(448+rng.Intn(64)))
		}
		return nil
	}
	return encoder.EncodeChunkToIVF(context.Background(), cfg, readFrame, nil)
}

// TestRecordedVerdictIsReusedNotRegated is the resume contract: once a work
// directory has a verdict, both entry points return it without touching the
// source (these calls would fail if they tried to encode sample chunks: the
// input path does not exist).
func TestRecordedVerdictIsReusedNotRegated(t *testing.T) {
	if !encoder.FGSTableSupported() {
		t.Skip("linked SVT-AV1 does not support grain tables")
	}
	workDir := t.TempDir()
	recorded := &perf.GrainTreatmentStats{
		Mode:            config.GrainTreatmentAuto,
		Treated:         true,
		ResolutionClass: "uhd",
		Denoise:         grainDenoiseFilter,
		MedianBPP:       0.1904,
		SampleChunks:    []int{20, 34, 49, 64, 79},
		SampleBPP:       []float64{0.18, 0.19, 0.1904, 0.20, 0.21},
	}
	saveTestGrainVerdict(t, workDir, recorded)

	in := GrainGateInput{InputPath: filepath.Join(workDir, "missing.mkv"), WorkDir: workDir, Info: uhdInfo(), Chunks: makeChunks(100, 120)}
	cfg := &EncodeConfig{}

	got, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentAuto, cfg, in)
	if err != nil {
		t.Fatalf("ResolveGrainTreatment: %v", err)
	}
	if got.Denoise != grainDenoiseFilter || got.Stats.Tier != "estimated" {
		t.Fatalf("resumed treatment = %+v", got)
	}
	if got.Stats.MedianBPP != recorded.MedianBPP || len(got.Stats.SampleChunks) != 5 {
		t.Errorf("recorded measurements were not carried through: %+v", got.Stats)
	}
	if _, err := os.Stat(got.TablePath); err != nil {
		t.Errorf("grain table was not materialized: %v", err)
	}

	// The manifest builder's view must agree without materializing anything.
	pre, err := RecordedGrainTreatment(config.GrainTreatmentAuto, cfg, in)
	if err != nil {
		t.Fatalf("RecordedGrainTreatment: %v", err)
	}
	if pre.Denoise != grainDenoiseFilter || pre.Stats.GrainTable != recorded.GrainTable {
		t.Errorf("recorded treatment = %+v", pre)
	}
	if pre.TablePath != "" {
		t.Error("RecordedGrainTreatment must not materialize a table")
	}
}

func TestGrainTreatmentModesWithoutGate(t *testing.T) {
	in := GrainGateInput{WorkDir: t.TempDir(), Info: uhdInfo()}

	off, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentOff, &EncodeConfig{}, in)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if off.Denoise != "" || off.Stats.Treated || off.Stats.Mode != config.GrainTreatmentOff {
		t.Errorf("off mode = %+v", off.Stats)
	}

	table := "/tmp/custom.tbl"
	override, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentAuto,
		&EncodeConfig{Denoise: "hqdn3d", GrainTable: &table}, in)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if override.Denoise != "hqdn3d" || override.TablePath != table || override.Stats.Mode != "override" {
		t.Errorf("override mode = %+v", override.Stats)
	}
	if _, err := os.Stat(filepath.Join(in.WorkDir, "grain-gate.json")); !os.IsNotExist(err) {
		t.Error("explicit overrides must not record a gate verdict")
	}
}

// A pillarboxed 1080p film (Mary Poppins crops 1920 -> 1792) must classify by
// its SOURCE width: post-crop width fell below the HD threshold and wrongly
// gated the title as "SD, never treated".
func TestGrainClassUsesSourceWidthNotCropped(t *testing.T) {
	in := GrainGateInput{
		WorkDir:  t.TempDir(),
		Info:     &video.Info{Width: 1920, Height: 1080, FPSNum: 24000, FPSDen: 1001, Frames: 12000},
		CropRect: &video.CropRect{X: 64, Y: 0, Width: 1792, Height: 1080},
	}
	off, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentOff, &EncodeConfig{}, in)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if off.Stats.ResolutionClass != "hd" {
		t.Errorf("resolution class = %q, want hd for a pillarboxed 1080p source", off.Stats.ResolutionClass)
	}
}

func TestGrainTreatmentSummary(t *testing.T) {
	if GrainTreatmentSummary(nil) != nil {
		t.Error("nil stats should produce no summary")
	}

	clean := GrainTreatmentSummary(&perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, GateCRF: 22,
		SampleBPP: []float64{0.01, 0.02}, MedianBPP: 0.015, LightBPPCutoff: uhdLightBPP,
	})
	if len(clean) != 1 || !strings.Contains(clean[0], "untreated") {
		t.Errorf("clean summary = %q", clean)
	}

	mean, worst := 9.71, 9.62
	treated := GrainTreatmentSummary(&perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, GateCRF: 22, Treated: true, Tier: "estimated",
		Denoise: grainDenoiseFilter, SampleBPP: []float64{0.19}, MedianBPP: 0.19,
		LightBPPCutoff: uhdLightBPP, DenoiseCeilingJODMean: &mean, DenoiseCeilingJODMin: &worst,
	})
	if len(treated) != 2 || !strings.Contains(treated[1], "fftdnoiz") || !strings.Contains(treated[1], "9.71") {
		t.Errorf("treated summary = %q", treated)
	}

	// A stage 2 verdict says what the fixed-CRF measurement could not settle
	// and what measuring at the target found.
	stage2 := GrainTreatmentSummary(&perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, GateCRF: 22, Treated: true, Tier: "estimated",
		Denoise: grainDenoiseFilter, SampleBPP: []float64{0.0477}, MedianBPP: 0.0477,
		AmbiguousBPPCutoff: uhdAmbiguousBPP, LightBPPCutoff: uhdLightBPP,
		GateStage: grainStageTQProbe, Stage2MedianBPP: 0.0919,
	})
	if len(stage2) != 3 || !strings.Contains(stage2[1], "ambiguous") || !strings.Contains(stage2[1], "0.0919") {
		t.Errorf("stage 2 summary = %q", stage2)
	}

	// A stage 2 that could not run says so, and the fixed-CRF verdict stands.
	fallback := GrainTreatmentSummary(&perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, GateCRF: 22, SampleBPP: []float64{0.0477}, MedianBPP: 0.0477,
		AmbiguousBPPCutoff: uhdAmbiguousBPP, LightBPPCutoff: uhdLightBPP,
		GateStage: grainStageBPP, Stage2Error: "no display model",
	})
	if len(fallback) != 2 || !strings.Contains(fallback[1], "no display model") || !strings.Contains(fallback[1], "untreated") {
		t.Errorf("stage 2 fallback summary = %q", fallback)
	}
}

// TestGrainStage2BandRouting is the routing contract: only a fixed-CRF median
// inside the ambiguous band is worth re-measuring at the target. Below it the
// title is cheap everywhere; at or above the treat cutoff stage 1 already
// decides.
func TestGrainStage2BandRouting(t *testing.T) {
	uhdAmbiguous, uhdLight := bppCutoffs(3840)
	hdAmbiguous, hdLight := bppCutoffs(1920)

	cases := []struct {
		name                  string
		bpp, ambiguous, light float64
		want                  bool
	}{
		{"uhd far below the band", 0.0099, uhdAmbiguous, uhdLight, false},
		{"uhd just below the floor", uhdAmbiguous - 1e-6, uhdAmbiguous, uhdLight, false},
		{"uhd at the floor", uhdAmbiguous, uhdAmbiguous, uhdLight, true},
		{"uhd American Hustle", 0.0477, uhdAmbiguous, uhdLight, true},
		{"uhd Meet the Parents", 0.0551, uhdAmbiguous, uhdLight, true},
		{"uhd at the treat cutoff", uhdLight, uhdAmbiguous, uhdLight, false},
		{"uhd Vacation", 0.1113, uhdAmbiguous, uhdLight, false},
		{"hd below the band", 0.0342, hdAmbiguous, hdLight, false},
		{"hd at the floor", hdAmbiguous, hdAmbiguous, hdLight, true},
		{"hd soms", 0.1744, hdAmbiguous, hdLight, true},
		{"hd at the treat cutoff", hdLight, hdAmbiguous, hdLight, false},
		{"hd Mary Poppins", 0.2706, hdAmbiguous, hdLight, false},
		{"sd has no band", 0.5, 0, 0, false},
	}
	for _, tc := range cases {
		if got := grainStage2Applies(tc.bpp, tc.ambiguous, tc.light); got != tc.want {
			t.Errorf("%s: grainStage2Applies(%.4f) = %v, want %v", tc.name, tc.bpp, got, tc.want)
		}
	}
}

// TestGrainStage2DecidesFromDeliveredBPP drives the stage 2 verdict from
// injected measurements: what the samples cost at the target is compared
// against the same cutoffs the fixed-CRF median was.
func TestGrainStage2DecidesFromDeliveredBPP(t *testing.T) {
	cases := []struct {
		name        string
		delivered   []float64
		wantTreated bool
	}{
		// American Hustle: 0.048 bpp at CRF 22 but 18.3 Mbps delivered
		// (0.092 bpp), the false negative stage 2 exists to catch.
		{"delivered bits clear the treat cutoff", []float64{0.070, 0.0919, 0.110}, true},
		// Meet the Parents: 0.055 at CRF 22 and 9.9 Mbps delivered, which
		// must stay untreated even though it measures alongside Hustle.
		{"delivered bits stay under the cutoff", []float64{0.040, 0.0497, 0.060}, false},
		{"delivered bits far above cutoff", []float64{0.100, 0.120, 0.140}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stats := ambiguousStage1Stats()
			samples := makeChunks(3, 200)
			next := 0
			measure := func(_ context.Context, _ chunk.Chunk) (float64, int, error) {
				bpp := tc.delivered[next]
				next++
				return bpp, 2, nil
			}
			applyGrainStage2(context.Background(), GrainGateInput{}, stats, samples, measure)

			if stats.Treated != tc.wantTreated {
				t.Errorf("treated = %v, want %v", stats.Treated, tc.wantTreated)
			}
			if stats.GateStage != grainStageTQProbe {
				t.Errorf("gate stage = %q, want %q", stats.GateStage, grainStageTQProbe)
			}
			if !reflect.DeepEqual(stats.Stage2DeliveredBPP, tc.delivered) {
				t.Errorf("delivered bpp = %v, want %v", stats.Stage2DeliveredBPP, tc.delivered)
			}
			if stats.Stage2MedianBPP != tc.delivered[1] {
				t.Errorf("median delivered bpp = %v, want %v", stats.Stage2MedianBPP, tc.delivered[1])
			}
			if stats.Stage2Probes != 6 {
				t.Errorf("probe count = %d, want 6", stats.Stage2Probes)
			}
			if stats.Stage2Error != "" {
				t.Errorf("unexpected stage 2 error %q", stats.Stage2Error)
			}
			// The fixed-CRF measurement is kept: it is what the stage 2
			// verdict has to be read against.
			if stats.MedianBPP != 0.0477 {
				t.Errorf("stage 1 median was overwritten: %v", stats.MedianBPP)
			}
		})
	}
}

// TestGrainStage2FallsBackToStage1OnError: stage 2 is a refinement, so a
// scorer failure leaves the fixed-CRF verdict (untreated, since stage 2 only
// runs below the treat line) rather than failing the encode.
func TestGrainStage2FallsBackToStage1OnError(t *testing.T) {
	stats := ambiguousStage1Stats()
	calls := 0
	measure := func(_ context.Context, _ chunk.Chunk) (float64, int, error) {
		calls++
		if calls == 2 {
			return 0, 1, errors.New("cvvdp: no display model")
		}
		return 0.0919, 2, nil
	}
	applyGrainStage2(context.Background(), GrainGateInput{}, stats, makeChunks(3, 200), measure)

	if stats.Treated {
		t.Error("failed refinement changed the stage 1 verdict")
	}
	if stats.GateStage != grainStageBPP {
		t.Errorf("gate stage = %q, want %q", stats.GateStage, grainStageBPP)
	}
	if !strings.Contains(stats.Stage2Error, "no display model") {
		t.Errorf("stage 2 error = %q", stats.Stage2Error)
	}
	if stats.Stage2DeliveredBPP != nil || stats.Stage2MedianBPP != 0 {
		t.Errorf("a failed stage 2 must not record a measurement: %v / %v", stats.Stage2DeliveredBPP, stats.Stage2MedianBPP)
	}
	if calls != 2 {
		t.Errorf("measured %d chunks, want a stop at the failure", calls)
	}
	// The probes that did run are still reported; only the verdict is dropped.
	if stats.Stage2Probes != 3 {
		t.Errorf("probe count = %d, want 3", stats.Stage2Probes)
	}
}

// TestGrainStage2Ladder covers the probe stepping and the interpolation the
// verdict is read from.
func TestGrainStage2Ladder(t *testing.T) {
	const target float32 = 9.55

	// One probe below the target: step down on the default slope, clamped.
	// 0.55 JOD at 0.025 JOD/CRF is 22 CRF, more than one step may move.
	next, ok := nextGrainStage2CRF([]stage2Probe{{crf: 26, score: 9.0}}, target)
	if !ok || next != grainStage2StartCRF-grainStage2MaxCRFStep {
		t.Errorf("clamped step = %v (ok=%v), want %v", next, ok, grainStage2StartCRF-grainStage2MaxCRFStep)
	}

	// One probe above the target: step up.
	if next, ok := nextGrainStage2CRF([]stage2Probe{{crf: 26, score: 9.65}}, target); !ok || next <= 26 {
		t.Errorf("step for an over-target probe = %v (ok=%v), want a higher CRF", next, ok)
	}

	// Two probes measure a 0.06 JOD/CRF slope, inside the clamp, so the third
	// step uses it: 0.05 JOD short of the target is 0.83 CRF, rounded to 0.75.
	two := []stage2Probe{{crf: 26, score: 9.0}, {crf: 16, score: 9.6}}
	if next, ok := nextGrainStage2CRF(two, target); !ok || next != 16.75 {
		t.Errorf("measured-slope step = %v (ok=%v), want 16.75", next, ok)
	}

	// A step that lands on an already probed CRF ends the ladder instead of
	// spending a probe on a repeat.
	repeat := []stage2Probe{{crf: 26, score: 9.0}, {crf: 16, score: 9.6}, {crf: 16.75, score: 9.55}}
	if _, ok := nextGrainStage2CRF(repeat, 9.6); ok {
		t.Error("a repeated CRF should end the ladder")
	}

	// Bracketing probes interpolate the delivered size at the target.
	bracket := []stage2Probe{{crf: 26, score: 9.0, bpp: 0.05}, {crf: 16, score: 9.75, bpp: 0.11}}
	bpp, ok := interpolateDeliveredBPP(bracket, target)
	if !ok || math.Abs(bpp-0.094) > 1e-3 {
		t.Errorf("interpolated bpp = %v (ok=%v), want ~0.094", bpp, ok)
	}
	if _, ok := interpolateDeliveredBPP([]stage2Probe{{score: 9.0, bpp: 0.05}, {score: 9.2, bpp: 0.06}}, target); ok {
		t.Error("probes that do not bracket the target must not interpolate")
	}

	// Out of probes without a bracket: the closest probe is the answer.
	if got := closestStage2Probe(bracket, 9.7); got.bpp != 0.11 {
		t.Errorf("closest probe bpp = %v, want 0.11", got.bpp)
	}
}

// TestGrainVerdictRoundTripsStage2Fields is the resume contract for stage 2:
// the recorded verdict carries the target-quality measurement, so a resumed
// run replays it instead of re-probing.
func TestGrainVerdictRoundTripsStage2Fields(t *testing.T) {
	if !encoder.FGSTableSupported() {
		t.Skip("linked SVT-AV1 does not support grain tables")
	}
	workDir := t.TempDir()
	recorded := &perf.GrainTreatmentStats{
		Mode:               config.GrainTreatmentAuto,
		Treated:            true,
		ResolutionClass:    "uhd",
		Denoise:            grainDenoiseFilter,
		GateCRF:            float64(grainGateCRF),
		SampleChunks:       []int{20, 34, 49},
		SampleBPP:          []float64{0.041, 0.0477, 0.052},
		MedianBPP:          0.0477,
		AmbiguousBPPCutoff: uhdAmbiguousBPP,
		LightBPPCutoff:     uhdLightBPP,
		GateStage:          grainStageTQProbe,
		Stage2DeliveredBPP: []float64{0.081, 0.0919, 0.102},
		Stage2MedianBPP:    0.0919,
		Stage2Probes:       7,
		Stage2Seconds:      431.5,
	}
	saveTestGrainVerdict(t, workDir, recorded)
	loaded, err := loadGrainVerdict(workDir)
	if err != nil || !reflect.DeepEqual(&loaded.GrainTreatmentStats, recorded) {
		t.Fatalf("loaded verdict = %+v (%v), want %+v", loaded, err, recorded)
	}

	// The JSON keys are the stats contract embedders read.
	data, err := os.ReadFile(filepath.Join(workDir, "grain-gate.json"))
	if err != nil {
		t.Fatalf("read verdict: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}
	for _, key := range []string{"gate_stage", "ambiguous_bpp_cutoff", "stage2_delivered_bpp", "stage2_median_bpp", "stage2_probes", "stage2_seconds"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("verdict is missing %q", key)
		}
	}

	// Resume replays the stage 2 verdict without re-gating (the input path
	// does not exist, so any probe would fail).
	in := GrainGateInput{InputPath: filepath.Join(workDir, "missing.mkv"), WorkDir: workDir, Info: uhdInfo(), Chunks: makeChunks(100, 120)}
	got, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentAuto, &EncodeConfig{}, in)
	if err != nil {
		t.Fatalf("ResolveGrainTreatment: %v", err)
	}
	if got.Stats.GateStage != grainStageTQProbe || got.Stats.Stage2MedianBPP != 0.0919 || got.Denoise != grainDenoiseFilter {
		t.Errorf("resumed stage 2 verdict = %+v", got.Stats)
	}
}

// ambiguousStage1Stats is a stage 1 verdict sitting in the UHD ambiguous band:
// American Hustle's measured median, which stage 1 leaves untreated.
func ambiguousStage1Stats() *perf.GrainTreatmentStats {
	return &perf.GrainTreatmentStats{
		Mode:               config.GrainTreatmentAuto,
		ResolutionClass:    "uhd",
		GateCRF:            float64(grainGateCRF),
		SampleBPP:          []float64{0.041, 0.0477, 0.052},
		MedianBPP:          0.0477,
		AmbiguousBPPCutoff: uhdAmbiguousBPP,
		LightBPPCutoff:     uhdLightBPP,
		GateStage:          grainStageBPP,
	}
}

func makeChunks(count, frames int) []chunk.Chunk {
	chunks := make([]chunk.Chunk, count)
	for i := range chunks {
		chunks[i] = chunk.Chunk{Idx: i, Start: i * frames, End: (i + 1) * frames}
	}
	return chunks
}

func sampleIndices(chunks []chunk.Chunk) []int {
	idx := make([]int, len(chunks))
	for i, ch := range chunks {
		idx[i] = ch.Idx
	}
	return idx
}

func uhdInfo() *video.Info {
	return &video.Info{Width: 3840, Height: 2160, FPSNum: 24000, FPSDen: 1001, Frames: 12000}
}
