package apply

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestRemapCommentaryIndices(t *testing.T) {
	original := []ripspec.CommentaryTrackRef{
		{Index: 0, Confidence: 0.7, Reason: "first"},
		{Index: 2, Confidence: 0.9, Reason: "second"},
		{Index: 4, Confidence: 0.5, Reason: "dropped"},
	}
	if got := remapCommentaryIndices(nil, original, nil); got != nil {
		t.Fatalf("empty kept indices = %+v", got)
	}
	if got := remapCommentaryIndices(nil, nil, []int{2}); got != nil {
		t.Fatalf("empty original = %+v", got)
	}
	got := remapCommentaryIndices(nil, original, []int{2, 3, 0})
	if len(got) != 2 || got[0].Index != 2 || got[0].Reason != "first" || got[0].Confidence != 0.7 || got[1].Index != 0 || got[1].Reason != "second" || got[1].Confidence != 0.9 {
		t.Fatalf("remapped tracks = %+v", got)
	}
	if got := remapCommentaryIndices(nil, original, []int{1, 3}); len(got) != 0 {
		t.Fatalf("dropped tracks retained: %+v", got)
	}
}

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
