package identification

import (
	"testing"

	"spindle/internal/disc"
)

func TestBuildPlaceholderAnnotationsCreatesEntries(t *testing.T) {
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60},
		{ID: 1, Duration: 22 * 60},
		{ID: 2, Duration: 22 * 60},
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
