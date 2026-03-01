package audioanalysis

import (
	"strings"
	"testing"

	"spindle/internal/media/ffprobe"
	"spindle/internal/ripspec"
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

func TestIsCommentaryCandidate(t *testing.T) {
	tests := []struct {
		name         string
		stream       ffprobe.Stream
		primaryIndex int
		want         bool
	}{
		{
			name:         "video stream",
			stream:       ffprobe.Stream{Index: 0, CodecType: "video", Channels: 2},
			primaryIndex: 1,
			want:         false,
		},
		{
			name:         "primary audio stream",
			stream:       ffprobe.Stream{Index: 1, CodecType: "audio", Channels: 2},
			primaryIndex: 1,
			want:         false,
		},
		{
			name:         "non-stereo audio",
			stream:       ffprobe.Stream{Index: 2, CodecType: "audio", Channels: 6},
			primaryIndex: 1,
			want:         false,
		},
		{
			name: "english stereo",
			stream: ffprobe.Stream{
				Index:     2,
				CodecType: "audio",
				Channels:  2,
				Tags:      map[string]string{"language": "eng"},
			},
			primaryIndex: 1,
			want:         true,
		},
		{
			name: "undefined language stereo",
			stream: ffprobe.Stream{
				Index:     2,
				CodecType: "audio",
				Channels:  2,
				Tags:      map[string]string{"language": "und"},
			},
			primaryIndex: 1,
			want:         true,
		},
		{
			name: "no language tag stereo",
			stream: ffprobe.Stream{
				Index:     2,
				CodecType: "audio",
				Channels:  2,
				Tags:      map[string]string{},
			},
			primaryIndex: 1,
			want:         true,
		},
		{
			name: "french stereo",
			stream: ffprobe.Stream{
				Index:     2,
				CodecType: "audio",
				Channels:  2,
				Tags:      map[string]string{"language": "fra"},
			},
			primaryIndex: 1,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCommentaryCandidate(tt.stream, tt.primaryIndex)
			if got != tt.want {
				t.Errorf("isCommentaryCandidate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindCommentaryCandidates(t *testing.T) {
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{Index: 1, CodecType: "audio", Channels: 8, Tags: map[string]string{"language": "eng"}},
		{Index: 2, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "eng"}},
		{Index: 3, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "eng"}},
		{Index: 4, CodecType: "audio", Channels: 6, Tags: map[string]string{"language": "fra"}},
		{Index: 5, CodecType: "subtitle"},
	}

	candidates := FindCommentaryCandidates(streams, 1) // primary is index 1

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].Index != 2 {
		t.Errorf("first candidate index = %d, want 2", candidates[0].Index)
	}
	if candidates[1].Index != 3 {
		t.Errorf("second candidate index = %d, want 3", candidates[1].Index)
	}
}

func TestBuildAudioStreamMappings(t *testing.T) {
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{Index: 1, CodecType: "audio", Tags: map[string]string{"title": "Main"}},
		{Index: 2, CodecType: "subtitle"},
		{Index: 3, CodecType: "audio", Tags: map[string]string{"title": "Commentary"}},
		{Index: 4, CodecType: "audio", Tags: map[string]string{"title": "Music Only"}},
	}

	commentaryIndices := map[int]bool{1: true} // output index 1 is commentary

	mappings := buildAudioStreamMappings(streams, commentaryIndices)

	if len(mappings) != 3 {
		t.Fatalf("expected 3 audio mappings, got %d", len(mappings))
	}

	// First audio (input 1) -> output 0
	if mappings[0].inputIndex != 1 || mappings[0].outputIndex != 0 {
		t.Errorf("mapping[0]: input=%d output=%d, want input=1 output=0",
			mappings[0].inputIndex, mappings[0].outputIndex)
	}
	if mappings[0].isCommentary {
		t.Error("mapping[0] should not be commentary (output index 0)")
	}

	// Second audio (input 3) -> output 1
	if mappings[1].inputIndex != 3 || mappings[1].outputIndex != 1 {
		t.Errorf("mapping[1]: input=%d output=%d, want input=3 output=1",
			mappings[1].inputIndex, mappings[1].outputIndex)
	}
	if !mappings[1].isCommentary {
		t.Error("mapping[1] should be commentary (output index 1)")
	}

	// Third audio (input 4) -> output 2
	if mappings[2].inputIndex != 4 || mappings[2].outputIndex != 2 {
		t.Errorf("mapping[2]: input=%d output=%d, want input=4 output=2",
			mappings[2].inputIndex, mappings[2].outputIndex)
	}
}

func TestHasAnyCommentary(t *testing.T) {
	tests := []struct {
		name     string
		mappings []audioStreamMapping
		want     bool
	}{
		{
			name:     "empty",
			mappings: []audioStreamMapping{},
			want:     false,
		},
		{
			name: "no commentary",
			mappings: []audioStreamMapping{
				{isCommentary: false},
				{isCommentary: false},
			},
			want: false,
		},
		{
			name: "has commentary",
			mappings: []audioStreamMapping{
				{isCommentary: false},
				{isCommentary: true},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasAnyCommentary(tt.mappings)
			if got != tt.want {
				t.Errorf("hasAnyCommentary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountCommentaryTracks(t *testing.T) {
	mappings := []audioStreamMapping{
		{isCommentary: false},
		{isCommentary: true},
		{isCommentary: false},
		{isCommentary: true},
	}

	got := countCommentaryTracks(mappings)
	if got != 2 {
		t.Errorf("countCommentaryTracks() = %d, want 2", got)
	}
}

func TestTruncateTranscript(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLen    int
		wantTrunc bool
	}{
		{
			name:      "short text",
			input:     "Hello world",
			maxLen:    100,
			wantTrunc: false,
		},
		{
			name:      "exact length",
			input:     "12345",
			maxLen:    5,
			wantTrunc: false,
		},
		{
			name:      "needs truncation",
			input:     "This is a very long transcript that exceeds the limit",
			maxLen:    20,
			wantTrunc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateTranscript(tt.input, tt.maxLen)
			if tt.wantTrunc {
				if len(got) > tt.maxLen+len("\n[truncated]") {
					t.Errorf("truncated result too long: %d", len(got))
				}
				if got[len(got)-11:] != "[truncated]" {
					t.Errorf("missing truncation marker")
				}
			} else if got != tt.input {
				t.Errorf("got %q, want %q", got, tt.input)
			}
		})
	}
}

func TestExtractYear(t *testing.T) {
	tests := []struct {
		name string
		env  *ripspec.Envelope
		want string
	}{
		{
			name: "nil envelope",
			env:  nil,
			want: "",
		},
		{
			name: "nil metadata",
			env:  &ripspec.Envelope{},
			want: "",
		},
		{
			name: "no year field",
			env:  &ripspec.Envelope{Metadata: ripspec.EnvelopeMetadata{Title: "Test"}},
			want: "",
		},
		{
			name: "year present",
			env:  &ripspec.Envelope{Metadata: ripspec.EnvelopeMetadata{Year: "2023"}},
			want: "2023",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractYear(tt.env)
			if got != tt.want {
				t.Errorf("extractYear() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildClassificationPrompt(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		year       string
		transcript string
		wantParts  []string
	}{
		{
			name:       "with year",
			title:      "Test Movie",
			year:       "2024",
			transcript: "Sample transcript",
			wantParts:  []string{"Title: Test Movie", "(2024)", "Sample transcript"},
		},
		{
			name:       "without year",
			title:      "Test Movie",
			year:       "",
			transcript: "Sample transcript",
			wantParts:  []string{"Title: Test Movie", "Sample transcript"},
		},
		{
			name:       "whitespace title",
			title:      "  Spaced Title  ",
			year:       "",
			transcript: "text",
			wantParts:  []string{"Title: Spaced Title"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildClassificationPrompt(tt.title, tt.year, tt.transcript)
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("BuildClassificationPrompt() = %q, missing %q", got, part)
				}
			}
		})
	}
}
