package identification

import "testing"

func TestSanitizeQueryCandidatePreservesParenthesesContent(t *testing.T) {
	input := "SOUTHPARK5_DISC1 (South Park Season 5 - Disc 1)"
	got := sanitizeQueryCandidate(input)
	want := "SOUTHPARK5 DISC1 South Park Season 5 Disc 1"
	if got != want {
		t.Fatalf("sanitizeQueryCandidate(%q) = %q, want %q", input, got, want)
	}
}

func TestDeriveShowHintStripsNoiseAndKeepsSeason(t *testing.T) {
	hint, season := deriveShowHint("SOUTHPARK5_DISC1 (South Park Season 5 - Disc 1)")
	if hint != "South Park" {
		t.Fatalf("expected hint to be 'South Park', got %q", hint)
	}
	if season != 5 {
		t.Fatalf("expected season 5, got %d", season)
	}
}

func TestBuildQueryListDeduplicatesSanitizedVariants(t *testing.T) {
	queries := buildQueryList("South Park Season 5 (Disc 1)", "South Park Season 5 Disc 1", "  South Park  ")
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d: %#v", len(queries), queries)
	}
	if queries[0] != "South Park Season 5 Disc 1" {
		t.Fatalf("unexpected first query %q", queries[0])
	}
	if queries[1] != "South Park" {
		t.Fatalf("unexpected second query %q", queries[1])
	}
}
