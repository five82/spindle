package chunkplan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestCachedPlanRejectsCorruptStateAndRestoresLegacyFields(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.y4m")
	if err := os.WriteFile(input, []byte("input identity"), 0600); err != nil {
		t.Fatal(err)
	}
	boundary, metadata := filepath.Join(dir, "plan.txt"), filepath.Join(dir, "meta.json")
	inf := &video.Info{Width: 32, Height: 24, Frames: 8, FPSNum: 25, FPSDen: 1}
	opts := Options{MaxFrames: 8, MinFrames: 2}
	result := Result{Boundaries: []int{0, 4}, NaturalCutFrames: []int{4}, MergedScenes: 2, Frames: 7}
	if err := writeMetadata(input, metadata, inf, opts, result); err != nil {
		t.Fatal(err)
	}
	if err := writeBoundaryFile(boundary, result.Boundaries); err != nil {
		t.Fatal(err)
	}
	cached, ok := loadCachedResult(input, boundary, metadata, inf, opts)
	if !ok || cached.MergedShortShots != 2 || cached.Frames != 7 || !reflect.DeepEqual(cached.Boundaries, result.Boundaries) || len(cached.BoundaryKinds) != 2 {
		t.Fatalf("cached plan = %+v, ok %v", cached, ok)
	}
	if _, ok := loadCachedResult(input, boundary, metadata, &video.Info{Width: 64}, opts); ok {
		t.Fatal("reused plan after source changed")
	}
	if err := os.WriteFile(boundary, []byte("bad boundary"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadCachedResult(input, boundary, metadata, inf, opts); ok {
		t.Fatal("accepted bad boundaries")
	}
	if err := os.WriteFile(metadata, []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadCachedResult(input, boundary, metadata, inf, opts); ok {
		t.Fatal("accepted bad metadata")
	}
}

func TestPlanToFileIfNeededReportsWriteFailures(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.y4m")
	if err := os.WriteFile(input, []byte("identity"), 0600); err != nil {
		t.Fatal(err)
	}
	inf := &video.Info{Width: 32, Height: 32, Frames: 0}
	missing := filepath.Join(dir, "missing", "plan.txt")
	if _, err := PlanToFileIfNeeded(context.Background(), input, missing, filepath.Join(dir, "meta.json"), inf, Options{}); err == nil {
		t.Fatal("wrote into missing directory")
	}
	boundary := filepath.Join(dir, "plan.txt")
	if _, err := PlanToFileIfNeeded(context.Background(), input, boundary, filepath.Join(dir, "missing", "meta.json"), inf, Options{}); err == nil {
		t.Fatal("wrote metadata into missing directory")
	}
	if _, err := PlanToFileIfNeeded(context.Background(), input, boundary, filepath.Join(dir, "meta.json"), nil, Options{}); err == nil {
		t.Fatal("nil video info accepted")
	}
	// Corrupt JSON can never become a valid cached result even if its fields happen to match.
	data, err := json.Marshal(Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadCachedResult(input, boundary, filepath.Join(dir, "meta.json"), inf, Options{}); ok {
		t.Fatal("accepted zero metadata")
	}
}
