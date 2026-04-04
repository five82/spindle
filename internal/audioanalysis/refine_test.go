package audioanalysis

import (
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestBuildKeptIndices_PrimaryFirst(t *testing.T) {
	got := buildKeptIndices(3, 1, []int{2})
	want := []int{1, 2}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d (%v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildKeptIndices_IgnoresOutOfRangeAdditionalKeep(t *testing.T) {
	got := buildKeptIndices(2, 0, []int{-1, 1, 9})
	want := []int{0, 1}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d (%v)", i, got[i], want[i], got)
		}
	}
}

func TestNeedsDispositionFix_WhenPrimaryNotFirst(t *testing.T) {
	result := &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "audio", Disposition: map[string]int{"default": 1}},
		{CodecType: "audio", Disposition: map[string]int{"default": 0}},
	}}
	if !needsDispositionFix(result, 1) {
		t.Fatal("expected disposition fix when selected primary is not first stream")
	}
}

func TestNeedsDispositionFix_WhenSecondaryIsDefault(t *testing.T) {
	result := &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "audio", Disposition: map[string]int{"default": 1}},
		{CodecType: "audio", Disposition: map[string]int{"default": 1}},
	}}
	if !needsDispositionFix(result, 0) {
		t.Fatal("expected disposition fix when secondary stream is also default")
	}
}

func TestNeedsDispositionFix_WhenAlreadyCorrect(t *testing.T) {
	result := &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "audio", Disposition: map[string]int{"default": 1}},
		{CodecType: "audio", Disposition: map[string]int{"default": 0}},
	}}
	if needsDispositionFix(result, 0) {
		t.Fatal("did not expect disposition fix when primary is first and sole default")
	}
}
