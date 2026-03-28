package audioanalysis

import "testing"

func TestCommentaryLabel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", "Commentary"},
		{"whitespace only", "   ", "Commentary"},
		{"generic title", "Stereo", "Stereo (Commentary)"},
		{"already has commentary", "Director's Commentary", "Director's Commentary"},
		{"case insensitive match", "COMMENTARY track", "COMMENTARY track"},
		{"mixed case match", "Cast commentary", "Cast commentary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commentaryLabel(tt.input)
			if got != tt.expected {
				t.Errorf("commentaryLabel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
