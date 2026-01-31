package audioanalysis

import (
	"testing"
)

func TestRemapCommentaryIndices(t *testing.T) {
	tests := []struct {
		name        string
		result      *CommentaryResult
		keptIndices []int
		wantIndices []int
	}{
		{
			name:        "nil result",
			result:      nil,
			keptIndices: []int{1, 9, 10, 11},
			wantIndices: nil,
		},
		{
			name: "empty commentary tracks",
			result: &CommentaryResult{
				CommentaryTracks: []CommentaryTrack{},
			},
			keptIndices: []int{1, 9, 10, 11},
			wantIndices: []int{},
		},
		{
			name: "remap three commentary tracks",
			result: &CommentaryResult{
				CommentaryTracks: []CommentaryTrack{
					{Index: 9, Confidence: 1.0, Reason: "director commentary"},
					{Index: 10, Confidence: 1.0, Reason: "editor commentary"},
					{Index: 11, Confidence: 1.0, Reason: "composer commentary"},
				},
			},
			keptIndices: []int{1, 9, 10, 11}, // primary=1, commentary=9,10,11
			wantIndices: []int{1, 2, 3},      // output audio indices after remux
		},
		{
			name: "single commentary track",
			result: &CommentaryResult{
				CommentaryTracks: []CommentaryTrack{
					{Index: 5, Confidence: 0.9, Reason: "commentary"},
				},
			},
			keptIndices: []int{1, 5},
			wantIndices: []int{1},
		},
		{
			name: "commentary index not in kept list",
			result: &CommentaryResult{
				CommentaryTracks: []CommentaryTrack{
					{Index: 99, Confidence: 1.0, Reason: "unknown"},
				},
			},
			keptIndices: []int{1, 9, 10},
			wantIndices: []int{99}, // unchanged since not found
		},
		{
			name: "empty kept indices",
			result: &CommentaryResult{
				CommentaryTracks: []CommentaryTrack{
					{Index: 9, Confidence: 1.0, Reason: "commentary"},
				},
			},
			keptIndices: []int{},
			wantIndices: []int{9}, // unchanged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RemapCommentaryIndices(tt.result, tt.keptIndices)

			if tt.result == nil {
				return
			}

			if len(tt.result.CommentaryTracks) != len(tt.wantIndices) {
				t.Fatalf("got %d tracks, want %d", len(tt.result.CommentaryTracks), len(tt.wantIndices))
			}

			for i, track := range tt.result.CommentaryTracks {
				if track.Index != tt.wantIndices[i] {
					t.Errorf("track %d: got index %d, want %d", i, track.Index, tt.wantIndices[i])
				}
			}
		})
	}
}

func TestCommentaryLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "Commentary"},
		{"  ", "Commentary"},
		{"Director Commentary", "Director Commentary"},
		{"director commentary", "director commentary"},
		{"Stereo", "Stereo (Commentary)"},
		{"English 2.0", "English 2.0 (Commentary)"},
		{"Has Commentary in title", "Has Commentary in title"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := commentaryLabel(tt.input)
			if got != tt.want {
				t.Errorf("commentaryLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
