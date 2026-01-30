package disc

import "testing"

func TestIsUnusableLabel(t *testing.T) {
	tests := []struct {
		label    string
		unusable bool
	}{
		// Empty or whitespace
		{"", true},
		{"   ", true},

		// Generic patterns
		{"LOGICAL_VOLUME_ID", true},
		{"VOLUME_ID_123", true},
		{"DVD_VIDEO", true},
		{"BLURAY", true},
		{"BD_ROM", true},
		{"UNTITLED", true},
		{"UNKNOWN DISC", true},
		{"VOLUME_1", true},
		{"VOLUME ID", true},
		{"DISK_1", true},
		{"TRACK_01", true},

		// All digits
		{"12345", true},
		{"0", true},
		{"999", true},

		// Very short codes
		{"A", true},
		{"AB", true},
		{"ABC", true},
		{"ABCD", true},
		{"X1", true},
		{"A_1", true},

		// Disc label patterns
		{"MOVIE_DISC_1", true},
		{"FILM_DISK_2", true},
		{"SOME_DISC", true},
		{"MY_DISK", true},

		// All uppercase with underscores, longer than 8 chars
		{"SOME_MOVIE_TITLE", true},
		{"THE_MATRIX_DISC_1", true},

		// Valid titles (should NOT be unusable)
		{"The Matrix", false},
		{"Inception", false},
		{"Avatar 2", false},
		{"THE_WOLVERINE", true}, // This is a disc label (uppercase + underscores > 8 chars)
		{"Wolverine", false},
		{"ABCDE", false}, // 5 chars, not matching short pattern
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			got := IsUnusableLabel(tc.label)
			if got != tc.unusable {
				t.Errorf("IsUnusableLabel(%q) = %v, want %v", tc.label, got, tc.unusable)
			}
		})
	}
}
