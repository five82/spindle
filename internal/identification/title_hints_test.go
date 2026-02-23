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

func TestDeriveShowHintStripsDescriptorNoise(t *testing.T) {
	cases := []struct {
		input  string
		hint   string
		season int
	}{
		{
			input:  "BATMAN_TV_S1_DISC_1 (Batman TV Series - Season 1: Disc 1)",
			hint:   "Batman",
			season: 1,
		},
		{
			input:  "SHOW_NAME (Some TV Show Season 2 Disc 3)",
			hint:   "Some",
			season: 2,
		},
		{
			input:  "The Complete Series",
			hint:   "",
			season: 0,
		},
	}
	for _, tc := range cases {
		hint, season := deriveShowHint(tc.input)
		if hint != tc.hint || season != tc.season {
			t.Errorf("deriveShowHint(%q) = (%q, %d), want (%q, %d)",
				tc.input, hint, season, tc.hint, tc.season)
		}
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

func TestSplitTitleYear(t *testing.T) {
	cases := []struct {
		input string
		title string
		year  int
	}{
		{input: "Goodfellas (1990)", title: "Goodfellas", year: 1990},
		{input: "Goodfellas 1990", title: "Goodfellas", year: 1990},
		{input: "Goodfellas (1990) ", title: "Goodfellas", year: 1990},
		{input: "Goodfellas (1990) (Director's Cut)", title: "Goodfellas (1990) (Director's Cut)", year: 0},
		{input: "Goodfellas", title: "Goodfellas", year: 0},
	}
	for _, tc := range cases {
		title, year := splitTitleYear(tc.input)
		if title != tc.title || year != tc.year {
			t.Fatalf("splitTitleYear(%q) = %q, %d want %q, %d", tc.input, title, year, tc.title, tc.year)
		}
	}
}

func TestExtractCanonicalTitle(t *testing.T) {
	cases := []struct {
		input     string
		canonical string
		label     string
	}{
		// Standard keydb format with canonical title in parentheses
		{
			input:     "STAR_TREK_TMP_DIRECTOR_EDITION (STAR TREK: THE MOTION PICTURE - DIRECTOR'S EDITION)",
			canonical: "STAR TREK: THE MOTION PICTURE - DIRECTOR'S EDITION",
			label:     "STAR_TREK_TMP_DIRECTOR_EDITION",
		},
		{
			input:     "GOODFELLAS_DISC1 (Goodfellas)",
			canonical: "Goodfellas",
			label:     "GOODFELLAS_DISC1",
		},
		// Year-only parentheses should not be treated as canonical title
		{
			input:     "Goodfellas (1990)",
			canonical: "",
			label:     "Goodfellas (1990)",
		},
		// No parentheses - return as label
		{
			input:     "GOODFELLAS",
			canonical: "",
			label:     "GOODFELLAS",
		},
		// Empty input
		{
			input:     "",
			canonical: "",
			label:     "",
		},
		// Disc info in parentheses should not be extracted
		{
			input:     "MOVIE (Disc 1)",
			canonical: "",
			label:     "MOVIE (Disc 1)",
		},
		// Short content in parentheses should not be extracted
		{
			input:     "MOVIE (AB)",
			canonical: "",
			label:     "MOVIE (AB)",
		},
		// Nested parentheses - extract outermost content
		{
			input:     "ALIENS (Aliens (1986) - Director's Cut)",
			canonical: "Aliens (1986) - Director's Cut",
			label:     "ALIENS",
		},
	}
	for _, tc := range cases {
		canonical, label := extractCanonicalTitle(tc.input)
		if canonical != tc.canonical || label != tc.label {
			t.Errorf("extractCanonicalTitle(%q) = (%q, %q), want (%q, %q)",
				tc.input, canonical, label, tc.canonical, tc.label)
		}
	}
}
