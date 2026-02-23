package identification

import (
	"testing"

	"spindle/internal/disc"
)

func TestBuildPlaceholderAnnotationsCreatesEntries(t *testing.T) {
	// Each title needs distinct tracks to produce a unique TitleHash
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60, Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 1, Duration: 23 * 60, Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 2, Duration: 24 * 60, Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
	}
	got := buildPlaceholderAnnotations(titles, 5)
	if len(got) != 3 {
		t.Fatalf("expected 3 annotations, got %d", len(got))
	}
	for _, id := range []int{0, 1, 2} {
		ann, ok := got[id]
		if !ok {
			t.Fatalf("expected annotation for title %d", id)
		}
		if ann.Season != 5 {
			t.Fatalf("title %d: expected season 5, got %d", id, ann.Season)
		}
		if ann.Episode != 0 {
			t.Fatalf("title %d: expected episode 0 (placeholder), got %d", id, ann.Episode)
		}
	}
}

func TestBuildPlaceholderAnnotationsDeduplicatesBySegmentMap(t *testing.T) {
	// Titles 0 and 2 share the same SegmentMap, so title 2 is deduplicated
	// even though they could have different TitleHash values.
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60, SegmentMap: "00001.m2ts", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 1, Duration: 23 * 60, SegmentMap: "00002.m2ts", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 2, Duration: 22 * 60, SegmentMap: "00001.m2ts", Tracks: []disc.Track{{StreamID: 2, Type: "video", CodecID: "V_MPEG2"}}},
	}
	got := buildPlaceholderAnnotations(titles, 1)
	if len(got) != 2 {
		t.Fatalf("expected 2 annotations (deduped), got %d", len(got))
	}
	if _, ok := got[0]; !ok {
		t.Fatal("expected annotation for title 0 (first occurrence)")
	}
	if _, ok := got[1]; !ok {
		t.Fatal("expected annotation for title 1 (unique)")
	}
	if _, ok := got[2]; ok {
		t.Fatal("title 2 should be deduplicated (same SegmentMap as title 0)")
	}
}

func TestBuildPlaceholderAnnotationsSameSegmentMapDifferentTitleHash(t *testing.T) {
	// Same SegmentMap but different track metadata (different TitleHash).
	// Should still deduplicate because SegmentMap takes priority.
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60, SegmentMap: "00010.m2ts", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 1, Duration: 22 * 60, SegmentMap: "00010.m2ts", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG2"}, {StreamID: 2, Type: "audio", CodecID: "A_AC3"}}},
	}
	got := buildPlaceholderAnnotations(titles, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 annotation (same SegmentMap deduped), got %d", len(got))
	}
	if _, ok := got[0]; !ok {
		t.Fatal("expected annotation for title 0 (first occurrence)")
	}
}

func TestBuildPlaceholderAnnotationsDifferentSegmentMapSameDuration(t *testing.T) {
	// Different SegmentMap values but same duration and tracks.
	// Both should be kept because they reference different content.
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60, SegmentMap: "00001.m2ts", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 1, Duration: 22 * 60, SegmentMap: "00002.m2ts", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
	}
	got := buildPlaceholderAnnotations(titles, 1)
	if len(got) != 2 {
		t.Fatalf("expected 2 annotations (different SegmentMap), got %d", len(got))
	}
}

func TestBuildPlaceholderAnnotationsEmptySegmentMapFallsBackToTitleHash(t *testing.T) {
	// When SegmentMap is empty (e.g. DVDs), fall back to TitleHash dedup.
	// Titles 0 and 2 have identical metadata → same TitleHash → deduped.
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60, Name: "ep1", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 1, Duration: 23 * 60, Name: "ep2", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
		{ID: 2, Duration: 22 * 60, Name: "ep1", Tracks: []disc.Track{{StreamID: 1, Type: "video", CodecID: "V_MPEG4"}}},
	}
	got := buildPlaceholderAnnotations(titles, 1)
	if len(got) != 2 {
		t.Fatalf("expected 2 annotations (TitleHash fallback dedup), got %d", len(got))
	}
	if _, ok := got[0]; !ok {
		t.Fatal("expected annotation for title 0")
	}
	if _, ok := got[2]; ok {
		t.Fatal("title 2 should be deduplicated (same TitleHash as title 0)")
	}
}

func TestBuildPlaceholderAnnotationsSkipsNonEpisodeDurations(t *testing.T) {
	titles := []disc.Title{
		{ID: 0, Duration: 9300}, // extras, should be skipped
		{ID: 1, Duration: 22 * 60},
	}
	got := buildPlaceholderAnnotations(titles, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(got))
	}
	if _, ok := got[0]; ok {
		t.Fatalf("title 0 (non-episode duration) should not have annotation")
	}
	if _, ok := got[1]; !ok {
		t.Fatalf("title 1 should have annotation")
	}
}

func TestBuildPlaceholderAnnotationsNilInput(t *testing.T) {
	if got := buildPlaceholderAnnotations(nil, 1); got != nil {
		t.Fatalf("expected nil for nil titles, got %v", got)
	}
	if got := buildPlaceholderAnnotations([]disc.Title{}, 1); got != nil {
		t.Fatalf("expected nil for empty titles, got %v", got)
	}
	if got := buildPlaceholderAnnotations([]disc.Title{{ID: 0, Duration: 22 * 60}}, 0); got != nil {
		t.Fatalf("expected nil for zero season, got %v", got)
	}
}

func TestPlaceholderOutputBasenameWithDiscNumber(t *testing.T) {
	got := PlaceholderOutputBasename("Show Name", 1, 2, 3)
	expect := "Show Name - S01 Disc 2 Episode 003"
	if got != expect {
		t.Fatalf("expected %q, got %q", expect, got)
	}
}

func TestPlaceholderOutputBasenameWithoutDiscNumber(t *testing.T) {
	got := PlaceholderOutputBasename("Show Name", 1, 0, 1)
	expect := "Show Name - S01 Episode 001"
	if got != expect {
		t.Fatalf("expected %q, got %q", expect, got)
	}
}

func TestPlaceholderOutputBasenameEmptyShow(t *testing.T) {
	got := PlaceholderOutputBasename("", 2, 0, 5)
	expect := "Manual Import - S02 Episode 005"
	if got != expect {
		t.Fatalf("expected %q, got %q", expect, got)
	}
}
