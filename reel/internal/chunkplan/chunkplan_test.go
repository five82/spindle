package chunkplan

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/five82/reel/internal/video"
)

func TestAcceptableTerminalEOF(t *testing.T) {
	if !acceptableTerminalEOF(95, 100) {
		t.Fatal("EOF after 95% of expected frames should be acceptable")
	}
	if acceptableTerminalEOF(94, 100) {
		t.Fatal("EOF before 95% of expected frames should not be acceptable")
	}
	if acceptableTerminalEOF(0, 100) {
		t.Fatal("EOF with no decoded frames should not be acceptable")
	}
}

func TestDetectNaturalCutsIncludesZeroAndHardCut(t *testing.T) {
	scores := make([]float64, 20)
	scores[10] = 0.9

	cuts := detectNaturalCuts(scores)
	want := []int{0, 10}
	if !reflect.DeepEqual(cuts, want) {
		t.Fatalf("detectNaturalCuts() = %v, want %v", cuts, want)
	}
}

func TestDetectNaturalCutsSuppressesTwoFrameFlash(t *testing.T) {
	scores := make([]float64, 20)
	scores[8] = 0.9
	scores[9] = 0.8

	cuts := detectNaturalCuts(scores)
	want := []int{0}
	if !reflect.DeepEqual(cuts, want) {
		t.Fatalf("detectNaturalCuts() = %v, want %v", cuts, want)
	}
}

func TestPlanBoundariesSplitsLongScenesEvenly(t *testing.T) {
	plan := planBoundaries([]int{0}, 520, 300, 1, 0, nil)
	want := []int{0, 260}
	if !reflect.DeepEqual(plan.Boundaries, want) {
		t.Fatalf("planBoundaries() = %v, want %v", plan.Boundaries, want)
	}
	if plan.SyntheticSplits != 1 {
		t.Fatalf("synthetic splits = %d, want 1", plan.SyntheticSplits)
	}
	if plan.MergedShortShots != 0 {
		t.Fatalf("merged short shots = %d, want 0", plan.MergedShortShots)
	}
}

func TestPlanBoundariesSplitsRepeatedlyWithoutTinyTail(t *testing.T) {
	plan := planBoundaries([]int{0}, 1000, 300, 1, 0, nil)
	want := []int{0, 250, 500, 750}
	if !reflect.DeepEqual(plan.Boundaries, want) {
		t.Fatalf("planBoundaries() = %v, want %v", plan.Boundaries, want)
	}
	if plan.SyntheticSplits != 3 {
		t.Fatalf("synthetic splits = %d, want 3", plan.SyntheticSplits)
	}
	if plan.MergedShortShots != 0 {
		t.Fatalf("merged short shots = %d, want 0", plan.MergedShortShots)
	}
}

func TestPlanBoundariesPacksShortShots(t *testing.T) {
	scores := make([]float64, 200)
	scores[60] = 0.9
	scores[70] = 0.2

	plan := planBoundaries([]int{0, 60, 70, 130}, 150, 80, 48, 0, scores)
	want := []int{0, 60, 130}
	if !reflect.DeepEqual(plan.Boundaries, want) {
		t.Fatalf("planBoundaries() = %v, want %v", plan.Boundaries, want)
	}
	if plan.SyntheticSplits != 0 {
		t.Fatalf("synthetic splits = %d, want 0", plan.SyntheticSplits)
	}
	if plan.MergedShortShots != 1 {
		t.Fatalf("merged short shots = %d, want 1", plan.MergedShortShots)
	}
}

func TestPlanBoundariesDoesNotPackBeyondMaxFrames(t *testing.T) {
	plan := planBoundaries([]int{0, 70, 80}, 150, 75, 48, 0, nil)
	want := []int{0, 70, 80}
	if !reflect.DeepEqual(plan.Boundaries, want) {
		t.Fatalf("planBoundaries() = %v, want %v", plan.Boundaries, want)
	}
	if plan.MergedShortShots != 0 {
		t.Fatalf("merged short shots = %d, want 0", plan.MergedShortShots)
	}
}

func TestPlanBoundariesTracksBoundaryKinds(t *testing.T) {
	plan := planBoundaries([]int{0, 70}, 220, 100, 1, 0, nil)
	wantBoundaries := []int{0, 70, 145}
	if !reflect.DeepEqual(plan.Boundaries, wantBoundaries) {
		t.Fatalf("plan boundaries = %v, want %v", plan.Boundaries, wantBoundaries)
	}
	wantKinds := []BoundaryKind{BoundaryKindStart, BoundaryKindNaturalShotCut, BoundaryKindSyntheticSplit}
	if !reflect.DeepEqual(plan.BoundaryKinds, wantKinds) {
		t.Fatalf("plan boundary kinds = %v, want %v", plan.BoundaryKinds, wantKinds)
	}
}

func TestPlanBoundariesPacksWeakCutsTowardTarget(t *testing.T) {
	scores := make([]float64, 240)
	scores[50] = 0.20
	scores[100] = 0.21
	scores[150] = 0.22
	plan := planBoundaries([]int{0, 50, 100, 150}, 220, 220, 1, 100, scores)
	want := []int{0}
	if !reflect.DeepEqual(plan.Boundaries, want) {
		t.Fatalf("plan boundaries = %v, want %v", plan.Boundaries, want)
	}
	if plan.MergedWeakCuts != 3 {
		t.Fatalf("merged weak cuts = %d, want 3", plan.MergedWeakCuts)
	}
}

func TestPlanBoundariesPreservesStrongCutWhenPackingToTarget(t *testing.T) {
	scores := make([]float64, 240)
	scores[50] = 0.20
	scores[100] = 0.90
	scores[150] = 0.21
	plan := planBoundaries([]int{0, 50, 100, 150}, 220, 220, 1, 100, scores)
	want := []int{0, 100}
	if !reflect.DeepEqual(plan.Boundaries, want) {
		t.Fatalf("plan boundaries = %v, want %v", plan.Boundaries, want)
	}
}

func TestInferBoundaryKindsPreservesNaturalCuts(t *testing.T) {
	got := inferBoundaryKinds([]int{0, 60, 120}, []int{60})
	want := []BoundaryKind{BoundaryKindStart, BoundaryKindNaturalShotCut, BoundaryKindSyntheticSplit}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inferBoundaryKinds() = %v, want %v", got, want)
	}
}

func TestSignatureFromFrameAppliesCrop(t *testing.T) {
	width, height, stride := 8, 4, 8
	data := make([]byte, stride*height)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			data[row*stride+col] = byte(col * 10)
		}
	}
	frame := &video.LumaFrame{Data: data, Stride: stride, Width: width, Height: height}
	crop := &video.CropRect{X: 2, Y: 0, Width: 4, Height: 4}

	sig, err := signatureFromFrame(frame, crop)
	if err != nil {
		t.Fatalf("signatureFromFrame returned error: %v", err)
	}
	if sig.Mean < 80 || sig.Mean > 200 {
		t.Fatalf("cropped signature mean = %.2f, want in cropped column range", sig.Mean)
	}
}

func TestReadLuma10SupportsP010Shift(t *testing.T) {
	data := make([]byte, 2)
	binary.LittleEndian.PutUint16(data, 512<<6)
	if got := readLuma10(data, 0, 0, true, 8); got != 512 {
		t.Fatalf("readLuma10() = %d, want 512", got)
	}
}

func TestPlanToFileIfNeededUsesMetadataCache(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.mkv")
	if err := os.WriteFile(input, []byte("not a real video"), 0644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Unix(100, 0)
	if err := os.Chtimes(input, modTime, modTime); err != nil {
		t.Fatal(err)
	}

	boundaryFile := filepath.Join(dir, "chunk-plan.txt")
	metadataFile := filepath.Join(dir, "chunk-plan.json")
	if err := os.WriteFile(boundaryFile, []byte("0\n25\n"), 0644); err != nil {
		t.Fatal(err)
	}
	inf := &video.Info{Width: 1920, Height: 1080, FPSNum: 24000, FPSDen: 1001, Frames: 50}
	result := Result{
		Boundaries:       []int{0, 25},
		BoundaryKinds:    []BoundaryKind{BoundaryKindStart, BoundaryKindNaturalShotCut},
		NaturalCuts:      1,
		NaturalCutFrames: []int{25},
		SyntheticSplits:  0,
	}
	if err := writeMetadata(input, metadataFile, inf, Options{MaxFrames: 30}, result); err != nil {
		t.Fatal(err)
	}

	got, ok := loadCachedResult(input, boundaryFile, metadataFile, inf, Options{MaxFrames: 30})
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !reflect.DeepEqual(got.Boundaries, result.Boundaries) {
		t.Fatalf("cached boundaries = %v, want %v", got.Boundaries, result.Boundaries)
	}
	if !reflect.DeepEqual(got.BoundaryKinds, result.BoundaryKinds) {
		t.Fatalf("cached boundary kinds = %v, want %v", got.BoundaryKinds, result.BoundaryKinds)
	}
}
