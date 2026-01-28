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

	best := selectBestResult(logger, "Example", resp, 0)
	if best == nil {
		t.Fatal("expected best result, got nil")
		return
	}
	if best.ID != 1 {
		t.Fatalf("expected ID 1, got %d", best.ID)
	}
}

func TestSelectBestResultPrefersExactMatchOverHigherScore(t *testing.T) {
	// Simulates "The Wolverine" scenario: exact match has lower score than
	// a more popular non-exact match, but should still be preferred.
	logger := logging.NewNop()
	resp := &tmdb.Response{Results: []tmdb.Result{
		{ID: 76170, Title: "The Wolverine", VoteAverage: 6.4, VoteCount: 10070},                // exact match, score ~11.7
		{ID: 263115, Title: "Logan", VoteAverage: 7.8, VoteCount: 20058},                       // higher score ~20.8, but not exact
		{ID: 447158, Title: "The Wolverine: Path of a Ronin", VoteAverage: 6.9, VoteCount: 25}, // contains, low votes
	}}

	best := selectBestResult(logger, "The Wolverine", resp, 5)
	if best == nil {
		t.Fatal("expected best result, got nil")
		return
	}
	if best.ID != 76170 {
		t.Fatalf("expected exact match ID 76170 (The Wolverine), got ID %d (%s)", best.ID, best.Title)
	}
}

func TestSelectBestResultRejectsLowConfidence(t *testing.T) {
	logger := logging.NewNop()
	resp := &tmdb.Response{Results: []tmdb.Result{
		{ID: 3, Title: "Example", VoteAverage: 1.5, VoteCount: 10},
	}}

	best := selectBestResult(logger, "Example", resp, 0)
	if best != nil {
		t.Fatalf("expected nil result for low rated match, got %+v", best)
	}
}

func TestSelectBestResultRejectsLowVoteCount(t *testing.T) {
	logger := logging.NewNop()
	resp := &tmdb.Response{Results: []tmdb.Result{
		{ID: 4, Title: "Example", VoteAverage: 7.0, VoteCount: 3},
	}}

	// With threshold of 5, should reject vote_count=3
	best := selectBestResult(logger, "Example", resp, 5)
	if best != nil {
		t.Fatalf("expected nil result for low vote count match, got %+v", best)
	}

	// With threshold of 0 (disabled), should accept
	best = selectBestResult(logger, "Example", resp, 0)
	if best == nil {
		t.Fatal("expected result with threshold disabled, got nil")
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
