package identification

import (
	"testing"

	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
)

func TestSelectBestResultExactMatch(t *testing.T) {
	logger := logging.NewNop()
	resp := &tmdb.Response{Results: []tmdb.Result{
		{ID: 1, Title: "Example", VoteAverage: 6.5, VoteCount: 120},
		{ID: 2, Title: "Something Else", VoteAverage: 8.0, VoteCount: 400},
	}}

	best := selectBestResult(logger, "Example", resp)
	if best == nil {
		t.Fatal("expected best result, got nil")
	}
	if best.ID != 1 {
		t.Fatalf("expected ID 1, got %d", best.ID)
	}
}

func TestSelectBestResultRejectsLowConfidence(t *testing.T) {
	logger := logging.NewNop()
	resp := &tmdb.Response{Results: []tmdb.Result{
		{ID: 3, Title: "Example", VoteAverage: 1.5, VoteCount: 10},
	}}

	best := selectBestResult(logger, "Example", resp)
	if best != nil {
		t.Fatalf("expected nil result for low rated match, got %+v", best)
	}
}

func TestScoreResult(t *testing.T) {
	score := scoreResult("example", tmdb.Result{Title: "Test Example", VoteAverage: 5.0, VoteCount: 100})
	if score <= 0 {
		t.Fatalf("expected positive score, got %f", score)
	}
}

func TestPickTitle(t *testing.T) {
	if got := pickTitle(tmdb.Result{Title: "Primary"}); got != "Primary" {
		t.Fatalf("expected primary title, got %q", got)
	}
	if got := pickTitle(tmdb.Result{Name: "Fallback"}); got != "Fallback" {
		t.Fatalf("expected fallback name, got %q", got)
	}
	if got := pickTitle(tmdb.Result{}); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
