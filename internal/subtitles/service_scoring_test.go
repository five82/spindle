package subtitles

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/subtitles/opensubtitles"
)

func TestRankSubtitleCandidatesPrefersBluRayOverWeb(t *testing.T) {
	subs := []opensubtitles.Subtitle{
		{FileID: 1, Language: "en", Release: "Michael.Clayton.2007.1080p.BluRay.x264", Downloads: 600, FeatureYear: 2007},
		{FileID: 2, Language: "en", Release: "Michael.Clayton.2007.WEB-DL.x264", Downloads: 6000, FeatureYear: 2007},
	}
	ordered := rankSubtitleCandidates(subs, []string{"en"}, SubtitleContext{MediaType: "movie", Year: "2007"})
	if len(ordered) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(ordered))
	}
	if ordered[0].subtitle.FileID != 1 {
		t.Fatalf("expected BluRay release to rank first, got file id %d", ordered[0].subtitle.FileID)
	}
}

func TestRankSubtitleCandidatesRespectsLanguageBuckets(t *testing.T) {
	subs := []opensubtitles.Subtitle{
		{FileID: 1, Language: "es", Release: "Movie.1080p.BluRay", Downloads: 200},
		{FileID: 2, Language: "en", Release: "Movie.1080p.BluRay", Downloads: 50},
		{FileID: 3, Language: "en", Release: "Movie.WEB-DL", Downloads: 150, AITranslated: true},
	}
	ordered := rankSubtitleCandidates(subs, []string{"en"}, SubtitleContext{MediaType: "movie"})
	if len(ordered) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(ordered))
	}
	if ordered[0].subtitle.FileID != 2 {
		t.Fatalf("expected human preferred language first, got %d", ordered[0].subtitle.FileID)
	}
	if ordered[1].subtitle.FileID != 3 {
		t.Fatalf("expected AI preferred language second, got %d", ordered[1].subtitle.FileID)
	}
	if ordered[2].subtitle.FileID != 1 {
		t.Fatalf("expected fallback language last, got %d", ordered[2].subtitle.FileID)
	}
}

func TestCheckSubtitleDurationRejectsSubtitleLongerThanVideo(t *testing.T) {
	// Subtitle is LONGER than video by 30s - this should be rejected
	path := filepath.Join(t.TempDir(), "sample.srt")
	contents := "1\n00:00:00,000 --> 00:01:30,000\nHello\n" // 90 seconds
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	delta, mismatch, err := checkSubtitleDuration(path, 60) // video is 60s
	if err != nil {
		t.Fatalf("checkSubtitleDuration returned error: %v", err)
	}
	if !mismatch {
		t.Fatalf("expected mismatch (subtitle longer than video), got none")
	}
	// delta = videoSeconds - last = 60 - 90 = -30
	if math.Abs(delta+30) > 0.1 {
		t.Fatalf("expected delta about -30, got %.2f", delta)
	}
}

func TestCheckSubtitleDurationAllowsCreditsGap(t *testing.T) {
	// Subtitle ends 5 minutes before video (normal credits gap) - should be allowed
	path := filepath.Join(t.TempDir(), "sample.srt")
	contents := "1\n00:00:00,000 --> 01:35:00,000\nHello\n" // 95 minutes = 5700s
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	delta, mismatch, err := checkSubtitleDuration(path, 6000) // video is 100 minutes
	if err != nil {
		t.Fatalf("checkSubtitleDuration returned error: %v", err)
	}
	if mismatch {
		t.Fatalf("expected no mismatch (credits gap is normal), got delta %.2f", delta)
	}
}

func TestCheckSubtitleDurationAllowsSmallDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.srt")
	contents := "1\n00:00:00,000 --> 01:00:05,000\nHello\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	delta, mismatch, err := checkSubtitleDuration(path, 3600)
	if err != nil {
		t.Fatalf("checkSubtitleDuration returned error: %v", err)
	}
	if mismatch {
		t.Fatalf("expected no mismatch, got delta %.2f", delta)
	}
}

func TestIsTitleMismatchRejectsWrongMovies(t *testing.T) {
	tests := []struct {
		expected  string
		candidate string
		mismatch  bool
	}{
		// Exact matches
		{"Toy Story 3", "Toy Story 3", false},
		{"toy story 3", "Toy Story 3", false},
		{"Toy Story 3", "toy story 3", false},

		// Contains relationship
		{"Toy Story 3", "Toy Story 3 3D", false},
		{"Toy Story", "Toy Story 3", false},

		// Partial word overlap (>= 50%) - stop words excluded
		{"The Dark Knight", "Dark Knight Rises", false},

		// Stop words should not count as matches
		// "The Freshman" vs "The NeverEnding Story II" - only "the" in common (filtered)
		{"The Freshman", "The NeverEnding Story II: The Next Chapter", true},
		{"The Freshman", "The Godfather 3", true},
		{"The Freshman", "The Rookie", true},
		{"A Beautiful Mind", "A Quiet Place", true},

		// Complete mismatch - should reject
		{"Toy Story 3", "Some Other Stories", true},
		{"Toy Story 3", "Finding Nemo", true},
		{"The Matrix", "Die Hard", true},

		// Empty strings - should not reject
		{"Toy Story 3", "", false},
		{"", "Some Movie", false},
	}

	for _, tt := range tests {
		got := isTitleMismatch(tt.expected, tt.candidate)
		if got != tt.mismatch {
			t.Errorf("isTitleMismatch(%q, %q) = %v, want %v", tt.expected, tt.candidate, got, tt.mismatch)
		}
	}
}

func TestRankSubtitleCandidatesFiltersMismatchedTitles(t *testing.T) {
	subs := []opensubtitles.Subtitle{
		{FileID: 1, Language: "en", FeatureTitle: "Toy Story 3", Downloads: 100},
		{FileID: 2, Language: "en", FeatureTitle: "Some Other Stories", Downloads: 200},
		{FileID: 3, Language: "en", FeatureTitle: "Toy Story 3 3D", Downloads: 50},
	}
	ctx := SubtitleContext{Title: "Toy Story 3", MediaType: "movie"}
	ordered := rankSubtitleCandidates(subs, []string{"en"}, ctx)

	// Should only have 2 candidates (mismatched title filtered out)
	if len(ordered) != 2 {
		t.Fatalf("expected 2 candidates after filtering, got %d", len(ordered))
	}

	// Verify the wrong movie was filtered
	for _, s := range ordered {
		if s.subtitle.FeatureTitle == "Some Other Stories" {
			t.Fatalf("mismatched title should have been filtered out")
		}
	}
}

func TestTitleMatchScorePenalizesMismatch(t *testing.T) {
	// Exact match should get bonus
	score, reason := titleMatchScore("Toy Story 3", "Toy Story 3")
	if score <= 0 {
		t.Errorf("exact match should have positive score, got %.1f", score)
	}
	if reason != "title=exact" {
		t.Errorf("expected reason 'title=exact', got %q", reason)
	}

	// Mismatch should get heavy penalty
	score, reason = titleMatchScore("Toy Story 3", "Finding Nemo")
	if score >= 0 {
		t.Errorf("mismatch should have negative score, got %.1f", score)
	}
	if reason != "title=mismatch" {
		t.Errorf("expected reason 'title=mismatch', got %q", reason)
	}
}

func TestIsTitleStrictMismatchRejectsFranchiseTitles(t *testing.T) {
	tests := []struct {
		name      string
		expected  string
		candidate string
		mismatch  bool
	}{
		// Exact matches - should pass
		{"exact match", "Star Trek: Generations", "Star Trek: Generations", false},
		{"exact match normalized", "star trek generations", "Star Trek: Generations", false},

		// Contains relationship - should pass
		{"contains", "Star Trek: Generations", "Star Trek: Generations - Special Edition", false},

		// Partial word overlap (franchise) - should FAIL strict matching
		{"franchise partial Star Trek III", "Star Trek: Generations", "Star Trek III: The Search for Spock", true},
		{"franchise partial Star Trek II", "Star Trek: Generations", "Star Trek II: The Wrath of Khan", true},
		{"franchise partial Fast Furious", "Fast & Furious 6", "Fast & Furious 7", true},
		{"franchise partial Avengers", "Avengers: Endgame", "Avengers: Age of Ultron", true},

		// Complete mismatch - should fail
		{"complete mismatch", "Star Trek: Generations", "Die Hard", true},

		// Empty strings - should pass (can't determine mismatch)
		{"empty candidate", "Star Trek: Generations", "", false},
		{"empty expected", "", "Star Trek III", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTitleStrictMismatch(tt.expected, tt.candidate)
			if got != tt.mismatch {
				t.Errorf("isTitleStrictMismatch(%q, %q) = %v, want %v", tt.expected, tt.candidate, got, tt.mismatch)
			}
		})
	}
}

func TestYearPenaltyForLargeDifferences(t *testing.T) {
	// Test that large year differences get strong penalties
	ctx := SubtitleContext{Title: "Star Trek: Generations", MediaType: "movie", Year: "1994"}

	// Subtitle from 1984 (10 year difference) should get year=wrong penalty
	sub1984 := opensubtitles.Subtitle{
		FileID:      1,
		FeatureYear: 1984,
		Release:     "1080p.BluRay",
	}
	score1984, reasons1984 := scoreSubtitleCandidate(sub1984, ctx)

	// Subtitle from 1994 (exact match) should get year=exact bonus
	sub1994 := opensubtitles.Subtitle{
		FileID:      2,
		FeatureYear: 1994,
		Release:     "1080p.BluRay",
	}
	score1994, reasons1994 := scoreSubtitleCandidate(sub1994, ctx)

	// 1994 should score significantly higher than 1984
	if score1994 <= score1984 {
		t.Errorf("expected 1994 (%v, %.1f) to score higher than 1984 (%v, %.1f)",
			reasons1994, score1994, reasons1984, score1984)
	}

	// Check that 1984 has year=wrong penalty
	hasWrongYear := false
	for _, r := range reasons1984 {
		if r == "year=wrong" {
			hasWrongYear = true
			break
		}
	}
	if !hasWrongYear {
		t.Errorf("expected year=wrong for 10-year difference, got reasons: %v", reasons1984)
	}

	// Check that 1994 has year=exact bonus
	hasExactYear := false
	for _, r := range reasons1994 {
		if r == "year=exact" {
			hasExactYear = true
			break
		}
	}
	if !hasExactYear {
		t.Errorf("expected year=exact for matching year, got reasons: %v", reasons1994)
	}
}

func TestFilterForcedSubtitleCandidatesRejectsFranchiseTitles(t *testing.T) {
	candidates := []scoredSubtitle{
		{subtitle: opensubtitles.Subtitle{FileID: 1, FeatureTitle: "Star Trek: Generations", Release: "correct.release"}},
		{subtitle: opensubtitles.Subtitle{FileID: 2, FeatureTitle: "Star Trek III: The Search for Spock", Release: "wrong.franchise.entry"}},
		{subtitle: opensubtitles.Subtitle{FileID: 3, FeatureTitle: "Star Trek: Generations - Extended", Release: "extended.edition"}},
	}

	filtered := filterForcedSubtitleCandidates(candidates, "Star Trek: Generations", nil)

	// Should only keep candidates with exact or contains match
	if len(filtered) != 2 {
		t.Fatalf("expected 2 candidates after filtering, got %d", len(filtered))
	}

	// Verify Star Trek III was filtered out
	for _, c := range filtered {
		if c.subtitle.FileID == 2 {
			t.Fatalf("Star Trek III should have been filtered out for forced subtitles")
		}
	}

	// Verify correct entries remain
	foundExact := false
	foundExtended := false
	for _, c := range filtered {
		if c.subtitle.FileID == 1 {
			foundExact = true
		}
		if c.subtitle.FileID == 3 {
			foundExtended = true
		}
	}
	if !foundExact || !foundExtended {
		t.Fatalf("expected both exact and extended matches to remain")
	}
}
