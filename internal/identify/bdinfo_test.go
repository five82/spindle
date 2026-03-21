package identify

import (
	"testing"
)

func TestParseBDInfoOutput_FullOutput(t *testing.T) {
	output := `Disc ID: ABCDEF0123456789
Volume Identifier: MOVIE_2019
Disc Title: The Great Movie 2019
BluRay detected: yes
AACS detected: yes
Provider data: Warner Bros. Home Entertainment
`
	result := parseBDInfoOutput(output)

	if result.DiscID != "ABCDEF0123456789" {
		t.Errorf("DiscID = %q, want %q", result.DiscID, "ABCDEF0123456789")
	}
	if result.VolumeIdentifier != "MOVIE_2019" {
		t.Errorf("VolumeIdentifier = %q", result.VolumeIdentifier)
	}
	if result.DiscName != "The Great Movie 2019" {
		t.Errorf("DiscName = %q", result.DiscName)
	}
	if !result.IsBluRay {
		t.Error("expected IsBluRay = true")
	}
	if !result.HasAACS {
		t.Error("expected HasAACS = true")
	}
	if result.Studio != "Warner Bros." {
		t.Errorf("Studio = %q, want %q", result.Studio, "Warner Bros.")
	}
	if result.Year != "2019" {
		t.Errorf("Year = %q, want %q", result.Year, "2019")
	}
}

func TestParseBDInfoOutput_DiscIDUppercased(t *testing.T) {
	output := "Disc ID: abcdef\n"
	result := parseBDInfoOutput(output)
	if result.DiscID != "ABCDEF" {
		t.Errorf("DiscID = %q, want %q", result.DiscID, "ABCDEF")
	}
}

func TestParseBDInfoOutput_NoBluray(t *testing.T) {
	output := `BluRay detected: no
AACS detected: no
`
	result := parseBDInfoOutput(output)
	if result.IsBluRay {
		t.Error("expected IsBluRay = false")
	}
	if result.HasAACS {
		t.Error("expected HasAACS = false")
	}
}

func TestParseBDInfoOutput_EmptyOutput(t *testing.T) {
	result := parseBDInfoOutput("")
	if result.DiscID != "" || result.IsBluRay || result.Year != "" {
		t.Error("expected empty result for empty output")
	}
}

func TestParseBDInfoOutput_YearFromVolume(t *testing.T) {
	output := `Volume Identifier: TITLE_2021
Disc Title: Some Title
`
	result := parseBDInfoOutput(output)
	// No year in disc title "Some Title", should extract from volume identifier.
	if result.Year != "2021" {
		t.Errorf("Year = %q, want %q", result.Year, "2021")
	}
}

func TestParseBDInfoOutput_YearPrefersDiscName(t *testing.T) {
	output := `Volume Identifier: TITLE_2020
Disc Title: Movie 2023 Edition
`
	result := parseBDInfoOutput(output)
	if result.Year != "2023" {
		t.Errorf("Year = %q, want %q (should prefer disc name)", result.Year, "2023")
	}
}

func TestMapStudio(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"Sony Pictures Home Entertainment", "Sony Pictures"},
		{"Warner Bros. Home Entertainment", "Warner Bros."},
		{"Universal Studios", "Universal Pictures"},
		{"disney", "Walt Disney Studios"},
		{"Paramount", "Paramount Pictures"},
		{"MGM Home Entertainment", "Metro-Goldwyn-Mayer"},
		{"Fox Home Entertainment", "20th Century Studios"},
		{"Lionsgate Films", "Lionsgate"},
		{"Unknown Provider Inc", "Unknown Provider Inc"}, // > 3 chars fallback
		{"AB", ""},                                        // <= 3 chars, no match
	}
	for _, tt := range tests {
		got := mapStudio(tt.provider)
		if got != tt.want {
			t.Errorf("mapStudio(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestExtractYear(t *testing.T) {
	tests := []struct {
		sources []string
		want    string
	}{
		{[]string{"Movie 2023", "VOL_2020"}, "2023"},
		{[]string{"No Year Here", "TITLE_1999"}, "1999"},
		{[]string{"NoYear", "NoYear"}, ""},
		{[]string{"Classic 1985"}, "1985"},
	}
	for _, tt := range tests {
		got := extractYear(tt.sources...)
		if got != tt.want {
			t.Errorf("extractYear(%v) = %q, want %q", tt.sources, got, tt.want)
		}
	}
}
