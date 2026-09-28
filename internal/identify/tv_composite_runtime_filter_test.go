package identify

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

func TestCompositeComponentsRejectsPartialSetsAndAcceptsThreeParts(t *testing.T) {
	title := func(id, duration int, segments string) tvTitleCandidate {
		return tvTitleCandidate{decisionIndex: id, title: ripspec.Title{ID: id, Duration: duration, SegmentMap: segments}}
	}
	composite := title(0, 3600, "1,2,3")
	if _, ok := compositeComponents(nil, composite); ok {
		t.Fatal("no components")
	}
	if _, ok := compositeComponents([]tvTitleCandidate{title(1, 1200, "1"), title(2, 1200, "2"), title(3, 1200, "4")}, composite); ok {
		t.Fatal("out-of-set segment accepted")
	}
	if _, ok := compositeComponents([]tvTitleCandidate{title(1, 600, "1"), title(2, 600, "2"), title(3, 600, "3")}, composite); ok {
		t.Fatal("wrong duration accepted")
	}
	parts, ok := compositeComponents([]tvTitleCandidate{title(1, 1200, "1"), title(2, 1200, "2"), title(3, 1200, "3")}, composite)
	if !ok || len(parts) != 3 {
		t.Fatalf("three-part composite: %+v %v", parts, ok)
	}
}

func TestRuntimeFilteringIncompatibleExpectationsAndCountCap(t *testing.T) {
	alive := []tvTitleCandidate{
		{decisionIndex: 0, title: ripspec.Title{ID: 2, Duration: 2400}},
		{decisionIndex: 1, title: ripspec.Title{ID: 1, Duration: 3600}},
	}
	expected := []tmdb.Episode{{Runtime: 20}}
	result := &tvTitleSelectionResult{Decisions: make([]tvTitleDecision, 2)}
	got := excludeRuntimeMismatches(alive, expected, result)
	if len(got) != 2 || !result.Ambiguous {
		t.Fatalf("incompatible runtimes should not discard every candidate: %+v %+v", got, result)
	}
	result = &tvTitleSelectionResult{Decisions: make([]tvTitleDecision, 2)}
	got = capToExpectedCount(alive, expected, result)
	if len(got) != 1 || got[0].title.ID != 2 || !result.Ambiguous {
		t.Fatalf("capped by episode count: %+v %+v", got, result)
	}
	if runtimeFit(2400, []int{0, 2400}) != 0 {
		t.Fatal("zero target should not affect runtime fit")
	}
	if runtimeFit(2400, nil) != 0 {
		t.Fatal("missing runtimes should not bias title ranking")
	}
	result = &tvTitleSelectionResult{Decisions: make([]tvTitleDecision, 2)}
	got = excludeRuntimeMismatches(alive, []tmdb.Episode{{Runtime: 40}}, result)
	if len(got) != 1 || got[0].title.ID != 2 || result.ExtraCount != 1 {
		t.Fatalf("discard mismatched runtime: %+v %+v", got, result)
	}
	if parts, ok := compositeComponents(nil, tvTitleCandidate{title: ripspec.Title{SegmentMap: "bad"}}); ok || len(parts) != 0 {
		t.Fatalf("bad segment map: %+v %v", parts, ok)
	}
	if _, ok := parseSegmentSet(" , "); ok {
		t.Fatal("empty segment list accepted")
	}
	if durationsLookCombined(0, 3600) || durationsLookCombined(1800, 0) {
		t.Fatal("missing runtime accepted")
	}
}
