package organizer

import (
	"testing"

	"spindle/internal/logging"
)

func TestValidateEditionFilenameNoEdition(t *testing.T) {
	// When edition is empty, validation should pass
	err := ValidateEditionFilename("/path/to/Movie.mkv", "", nil)
	if err != nil {
		t.Fatalf("expected no error for empty edition, got: %v", err)
	}
}

func TestValidateEditionFilenameEmptyPath(t *testing.T) {
	// Empty path should fail validation
	err := ValidateEditionFilename("", "Director's Cut", nil)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateEditionFilenamePresent(t *testing.T) {
	// Edition should be found in filename
	logger := logging.NewNop()
	err := ValidateEditionFilename("/path/to/Movie (2020) - Director's Cut.mkv", "Director's Cut", logger)
	if err != nil {
		t.Fatalf("expected no error when edition is present, got: %v", err)
	}
}

func TestValidateEditionFilenameMissing(t *testing.T) {
	// Edition missing from filename should fail
	logger := logging.NewNop()
	err := ValidateEditionFilename("/path/to/Movie (2020).mkv", "Director's Cut", logger)
	if err == nil {
		t.Fatal("expected error when edition is missing from filename")
	}
}

func TestValidateEditionFilenameWrongFormat(t *testing.T) {
	// Edition present but in wrong format should fail
	logger := logging.NewNop()

	// Missing " - " prefix
	err := ValidateEditionFilename("/path/to/Movie (2020) Director's Cut.mkv", "Director's Cut", logger)
	if err == nil {
		t.Fatal("expected error when edition format is incorrect")
	}
}

func TestValidateEditionFilenameVariousEditions(t *testing.T) {
	tests := []struct {
		path    string
		edition string
		valid   bool
	}{
		{"/path/to/Movie - Extended.mkv", "Extended", true},
		{"/path/to/Movie - Theatrical.mkv", "Theatrical", true},
		{"/path/to/Movie - Unrated.mkv", "Unrated", true},
		{"/path/to/Movie - 4K Remaster.mkv", "4K Remaster", true},
		{"/path/to/Movie - Extended.mkv", "Theatrical", false},
		{"/path/to/Movie.mkv", "Extended", false},
	}

	logger := logging.NewNop()
	for _, tt := range tests {
		err := ValidateEditionFilename(tt.path, tt.edition, logger)
		if tt.valid && err != nil {
			t.Errorf("ValidateEditionFilename(%q, %q) = error %v, want nil", tt.path, tt.edition, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateEditionFilename(%q, %q) = nil, want error", tt.path, tt.edition)
		}
	}
}

func TestValidateEditionFilenameWhitespace(t *testing.T) {
	// Whitespace-only edition should be treated as empty
	err := ValidateEditionFilename("/path/to/Movie.mkv", "   ", nil)
	if err != nil {
		t.Fatalf("expected no error for whitespace-only edition, got: %v", err)
	}
}
