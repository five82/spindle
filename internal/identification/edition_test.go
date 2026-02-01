package identification

import (
	"testing"
)

func TestExtractKnownEdition(t *testing.T) {
	tests := []struct {
		name      string
		discTitle string
		wantLabel string
		wantFound bool
	}{
		// Director's versions
		{
			name:      "director's cut uppercase",
			discTitle: "BLADE_RUNNER_DIRECTORS_CUT",
			wantLabel: "Director's Cut",
			wantFound: true,
		},
		{
			name:      "director's edition",
			discTitle: "Star Trek The Motion Picture Director's Edition",
			wantLabel: "Director's Cut",
			wantFound: true,
		},
		{
			name:      "director alone",
			discTitle: "ALIEN_DIRECTOR",
			wantLabel: "Director's Cut",
			wantFound: true,
		},

		// Extended versions
		{
			name:      "extended edition",
			discTitle: "LORD_OF_THE_RINGS_EXTENDED_EDITION",
			wantLabel: "Extended Edition",
			wantFound: true,
		},
		{
			name:      "extended cut",
			discTitle: "Batman v Superman Extended Cut",
			wantLabel: "Extended Edition",
			wantFound: true,
		},
		{
			name:      "extended alone",
			discTitle: "AVATAR_EXTENDED",
			wantLabel: "Extended Edition",
			wantFound: true,
		},

		// Unrated/Uncut
		{
			name:      "unrated",
			discTitle: "ANCHORMAN_UNRATED",
			wantLabel: "Unrated",
			wantFound: true,
		},
		{
			name:      "uncut edition",
			discTitle: "HALLOWEEN_UNCUT_EDITION",
			wantLabel: "Uncut",
			wantFound: true,
		},

		// Remastered
		{
			name:      "remastered",
			discTitle: "E.T._REMASTERED",
			wantLabel: "Remastered",
			wantFound: true,
		},

		// Special editions
		{
			name:      "special edition",
			discTitle: "STAR_WARS_SPECIAL_EDITION",
			wantLabel: "Special Edition",
			wantFound: true,
		},

		// Anniversary editions
		{
			name:      "anniversary edition",
			discTitle: "JAWS_25TH_ANNIVERSARY_EDITION",
			wantLabel: "Anniversary Edition",
			wantFound: true,
		},

		// Theatrical
		{
			name:      "theatrical cut",
			discTitle: "APOCALYPSE_NOW_THEATRICAL_CUT",
			wantLabel: "Theatrical",
			wantFound: true,
		},

		// Final Cut
		{
			name:      "final cut",
			discTitle: "BLADE_RUNNER_FINAL_CUT",
			wantLabel: "Final Cut",
			wantFound: true,
		},

		// Redux
		{
			name:      "redux",
			discTitle: "APOCALYPSE_NOW_REDUX",
			wantLabel: "Redux",
			wantFound: true,
		},

		// IMAX
		{
			name:      "imax",
			discTitle: "DUNE_IMAX",
			wantLabel: "IMAX",
			wantFound: true,
		},

		// No edition detected
		{
			name:      "standard release",
			discTitle: "LOGAN",
			wantLabel: "",
			wantFound: false,
		},
		{
			name:      "year in title",
			discTitle: "BLADE_RUNNER_2049",
			wantLabel: "",
			wantFound: false,
		},
		{
			name:      "empty string",
			discTitle: "",
			wantLabel: "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLabel, gotFound := ExtractKnownEdition(tt.discTitle)
			if gotLabel != tt.wantLabel {
				t.Errorf("ExtractKnownEdition(%q) label = %q, want %q", tt.discTitle, gotLabel, tt.wantLabel)
			}
			if gotFound != tt.wantFound {
				t.Errorf("ExtractKnownEdition(%q) found = %v, want %v", tt.discTitle, gotFound, tt.wantFound)
			}
		})
	}
}

func TestExtractEditionLabel(t *testing.T) {
	tests := []struct {
		name      string
		discTitle string
		tmdbTitle string
		wantLabel string
	}{
		{
			name:      "noir edition",
			discTitle: "LOGAN_NOIR",
			tmdbTitle: "Logan",
			wantLabel: "Noir",
		},
		{
			name:      "multiple extra words",
			discTitle: "MOVIE_SPECIAL_FEATURE",
			tmdbTitle: "Movie",
			wantLabel: "Special Feature",
		},
		{
			name:      "no difference",
			discTitle: "Logan",
			tmdbTitle: "Logan",
			wantLabel: "",
		},
		{
			name:      "year difference ignored",
			discTitle: "BLADE_RUNNER_2049",
			tmdbTitle: "Blade Runner",
			wantLabel: "",
		},
		{
			name:      "common words ignored",
			discTitle: "THE_MATRIX_AND",
			tmdbTitle: "The Matrix",
			wantLabel: "",
		},
		{
			name:      "empty disc title",
			discTitle: "",
			tmdbTitle: "Movie",
			wantLabel: "",
		},
		{
			name:      "empty tmdb title",
			discTitle: "Movie",
			tmdbTitle: "",
			wantLabel: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractEditionLabel(tt.discTitle, tt.tmdbTitle)
			if got != tt.wantLabel {
				t.Errorf("ExtractEditionLabel(%q, %q) = %q, want %q", tt.discTitle, tt.tmdbTitle, got, tt.wantLabel)
			}
		})
	}
}

func TestNormalizeEditionLabel(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "underscores to spaces",
			raw:  "DIRECTORS_CUT",
			want: "Directors Cut",
		},
		{
			name: "already proper case",
			raw:  "Noir",
			want: "Noir",
		},
		{
			name: "all lowercase",
			raw:  "extended edition",
			want: "Extended Edition",
		},
		{
			name: "empty string",
			raw:  "",
			want: "",
		},
		{
			name: "whitespace only",
			raw:  "   ",
			want: "",
		},
		{
			name: "multiple underscores",
			raw:  "SPECIAL___EDITION",
			want: "Special Edition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeEditionLabel(tt.raw)
			if got != tt.want {
				t.Errorf("NormalizeEditionLabel(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestHasAmbiguousEditionMarker(t *testing.T) {
	tests := []struct {
		name      string
		discTitle string
		tmdbTitle string
		want      bool
	}{
		{
			name:      "known pattern - not ambiguous",
			discTitle: "MOVIE_DIRECTORS_CUT",
			tmdbTitle: "Movie",
			want:      false,
		},
		{
			name:      "unknown extra content - ambiguous",
			discTitle: "LOGAN_NOIR",
			tmdbTitle: "Logan",
			want:      true,
		},
		{
			name:      "no extra content - not ambiguous",
			discTitle: "Logan",
			tmdbTitle: "Logan",
			want:      false,
		},
		{
			name:      "only common words extra - not ambiguous",
			discTitle: "THE_MOVIE_AND",
			tmdbTitle: "The Movie",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasAmbiguousEditionMarker(tt.discTitle, tt.tmdbTitle)
			if got != tt.want {
				t.Errorf("HasAmbiguousEditionMarker(%q, %q) = %v, want %v", tt.discTitle, tt.tmdbTitle, got, tt.want)
			}
		})
	}
}

func TestIsYearLike(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"2049", true},
		{"1984", true},
		{"2001", true},
		{"999", false},
		{"20491", false},
		{"NOIR", false},
		{"2O49", false}, // letter O instead of zero
	}

	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := isYearLike(tt.word)
			if got != tt.want {
				t.Errorf("isYearLike(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}
