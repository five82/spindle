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

	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/encoder"
	"github.com/five82/reel/internal/grain"
	"github.com/five82/reel/internal/perf"
)

func testGrainEstimate(t *testing.T) grain.Estimate {
	t.Helper()
	const w, h = 512, 256
	e, err := grain.New(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	rng := rand.New(rand.NewSource(82))
	src, dst := make([]byte, w*h*3), make([]byte, w*h*3)
	for frame := range 4 {
		var previous float64
		for i := range len(src) / 2 {
			clean := 512
			if i < w*h {
				clean = 160 + 640*(i%w)/w
			}
			n := rng.NormFloat64()*8 + previous*0.4
			previous = n
			binary.LittleEndian.PutUint16(dst[2*i:], uint16(clean))
			binary.LittleEndian.PutUint16(src[2*i:], uint16(math.Round(float64(clean)+n)))
		}
		if err := e.Observe(frame, src, dst); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func saveTestGrainVerdict(t *testing.T, dir string, stats *perf.GrainTreatmentStats) *grainVerdict {
	t.Helper()
	v := &grainVerdict{GrainTreatmentStats: *stats}
	if v.Treated {
		v.setEstimate(testGrainEstimate(t))
	}
	*stats = v.GrainTreatmentStats
	if err := saveGrainVerdict(dir, v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGrainModelResumeUsesExactSavedBytes(t *testing.T) {
	if !encoder.FGSTableSupported() {
		t.Skip("linked SVT-AV1 does not support grain tables")
	}
	dir := t.TempDir()
	stats := &perf.GrainTreatmentStats{Mode: config.GrainTreatmentAuto, Treated: true, Denoise: grainDenoiseFilter}
	v := saveTestGrainVerdict(t, dir, stats)
	// An estimator update must not invalidate a resumable, already fitted model.
	v.Estimation.Version = "previous-estimator-version"
	if err := saveGrainVerdict(dir, v); err != nil {
		t.Fatal(err)
	}
	originalRecord, err := os.ReadFile(grainVerdictPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	in := GrainGateInput{WorkDir: dir, InputPath: "does-not-exist", Info: uhdInfo()}
	for _, materialized := range []string{"missing", "corrupt"} {
		t.Run(materialized, func(t *testing.T) {
			path := filepath.Join(dir, "grain-estimated.tbl")
			if materialized == "corrupt" {
				if err := os.WriteFile(path, []byte("stale table"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentAuto, &EncodeConfig{}, in)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(got.TablePath)
			if err != nil || string(data) != v.Table {
				t.Fatalf("materialized model differs from checkpoint: %v", err)
			}
			if !got.Stats.Reused || !reflect.DeepEqual(got.Stats.Estimation, v.Estimation) || got.Stats.GrainTable != v.GrainTable {
				t.Fatalf("resume changed estimation evidence: %+v", got.Stats)
			}
			record, err := os.ReadFile(grainVerdictPath(dir))
			if err != nil || string(record) != string(originalRecord) {
				t.Fatalf("resume modified the authoritative model record: %v", err)
			}
		})
	}
}

func TestGrainVerdictFailsClosed(t *testing.T) {
	valid := saveTestGrainVerdict(t, t.TempDir(), &perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, Treated: true, Denoise: grainDenoiseFilter,
	})
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"truncated", "missing-model", "checksum", "identity", "untreated-with-model"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			var v grainVerdict
			if err := json.Unmarshal(data, &v); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing-model":
				v.Estimation, v.Table, v.GrainTable = nil, "", "grain-med"
			case "checksum":
				v.Table += "corruption"
			case "identity":
				v.GrainTable = "estimated:other"
			case "untreated-with-model":
				v.Treated = false
			}
			bad, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "truncated" {
				bad = bad[:len(bad)/2]
			}
			if err := os.WriteFile(grainVerdictPath(dir), bad, 0600); err != nil {
				t.Fatal(err)
			}
			in := GrainGateInput{WorkDir: dir, Info: uhdInfo()}
			if _, err := RecordedGrainTreatment(config.GrainTreatmentAuto, &EncodeConfig{}, in); err == nil {
				t.Fatal("invalid checkpoint must not become a new gate decision")
			}
			if _, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentAuto, &EncodeConfig{}, in); err == nil {
				t.Fatal("invalid model must fail, not substitute a table")
			}
		})
	}
}

func TestIncompleteGrainVerdictIsNotPublished(t *testing.T) {
	dir := t.TempDir()
	v := &grainVerdict{GrainTreatmentStats: perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, Treated: true, Denoise: grainDenoiseFilter,
	}}
	if err := saveGrainVerdict(dir, v); err == nil {
		t.Fatal("published a treated verdict before estimation completed")
	}
	if _, err := os.Stat(grainVerdictPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete verdict left a checkpoint: %v", err)
	}
}

func TestGrainAnalysisRequiresPairsAndHonorsCancellation(t *testing.T) {
	in := GrainGateInput{WorkDir: t.TempDir(), Info: uhdInfo()}
	stats := &perf.GrainTreatmentStats{Treated: true, Denoise: grainDenoiseFilter, SampleChunks: []int{0}}
	if _, err := estimateGrainAndCeiling(context.Background(), in, stats); err == nil || !strings.Contains(err.Error(), "paired-frame") {
		t.Fatalf("missing pair producer should fail explicitly: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := estimateGrainAndCeiling(ctx, in, stats); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(grainVerdictPath(in.WorkDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed analysis wrote a verdict: %v", err)
	}
}

func TestGrainModelSummary(t *testing.T) {
	stats := &perf.GrainTreatmentStats{
		Mode: config.GrainTreatmentAuto, Treated: true, Denoise: grainDenoiseFilter,
		SampleBPP: []float64{0.2}, Estimation: &perf.GrainEstimationStats{Patches: 768, Frames: make([]int, 48), Seconds: 0.5},
	}
	text := strings.Join(GrainTreatmentSummary(stats), " ")
	for _, want := range []string{"source-matched", "768 patches", "48 frames", "0.50s"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q: %s", want, text)
		}
	}
}
