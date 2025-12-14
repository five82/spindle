package commentaryid

import (
	"strings"
	"testing"
)

func TestIsSameAsPrimaryRequiresHighPurity(t *testing.T) {
	primary := "this is a long enough primary transcript sample with many tokens to satisfy the minimum token threshold for similarity windows in the classifier logic"
	duplicate := "this is a long enough primary transcript sample with many tokens to satisfy the minimum token threshold for similarity windows in the classifier logic"
	mixed := "this is a long enough primary transcript sample with many tokens to satisfy the minimum token threshold for similarity windows in the classifier logic plus director commentary about production choices and shooting locations"

	w1 := compareWindow(primary, duplicate)
	if !w1.valid {
		t.Fatalf("expected window to be valid")
	}
	if !isSameAsPrimary([]similarityWindow{w1}) {
		t.Fatalf("expected exact duplicate to be same-as-primary")
	}

	w2 := compareWindow(primary, mixed)
	if !w2.valid {
		t.Fatalf("expected window to be valid")
	}
	if isSameAsPrimary([]similarityWindow{w2}) {
		t.Fatalf("expected mixed transcript to not be same-as-primary (purity=%0.3f coverage=%0.3f cosine=%0.3f)", w2.purity, w2.coverage, w2.cosine)
	}
}

func TestLikelyMusicOnlyUsesTokenFloor(t *testing.T) {
	if !likelyMusicOnly("") {
		t.Fatalf("expected empty transcript to be music-only")
	}
	long := strings.Repeat("dialogue ", 50)
	if likelyMusicOnly(long) {
		t.Fatalf("expected long speech sample to not be music-only")
	}
}
