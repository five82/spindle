package subtitle

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

func TestParseSRT_Basic(t *testing.T) {
	content := `1
00:00:01,000 --> 00:00:03,000
Hello world

2
00:00:05,000 --> 00:00:07,000
Second cue
`
	cues := srtutil.Parse(content)
	if len(cues) != 2 {
		t.Fatalf("expected 2 cues, got %d", len(cues))
	}
	if cues[0].Text != "Hello world" {
		t.Errorf("cue 0 text = %q", cues[0].Text)
	}
	if cues[0].Start != 1.0 {
		t.Errorf("cue 0 start = %f, want 1.0", cues[0].Start)
	}
	if cues[0].End != 3.0 {
		t.Errorf("cue 0 end = %f, want 3.0", cues[0].End)
	}
	if cues[1].Index != 2 {
		t.Errorf("cue 1 index = %d, want 2", cues[1].Index)
	}
}

func TestFormatSRT_Roundtrip(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 1.0, End: 3.0, Text: "Hello"},
		{Index: 2, Start: 5.5, End: 7.5, Text: "World"},
	}
	output := srtutil.Format(cues)
	reparsed := srtutil.Parse(output)
	if len(reparsed) != 2 {
		t.Fatalf("roundtrip produced %d cues, want 2", len(reparsed))
	}
	if reparsed[0].Text != "Hello" {
		t.Errorf("roundtrip cue 0 text = %q", reparsed[0].Text)
	}
}

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Thank You!", "thank you"},
		{"  Hello   World  ", "hello world"},
		{"Line one\nLine two", "line one line two"},
		{"you.", "you"},
	}
	for _, tt := range tests {
		got := normalizeText(tt.input)
		if got != tt.want {
			t.Errorf("normalizeText(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRemoveIsolatedArtifacts_RepeatedPhrase(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Static noise"},
		{Index: 2, Start: 25, End: 27, Text: "Static noise"},
		{Index: 3, Start: 40, End: 42, Text: "Static noise"},
		{Index: 4, Start: 55, End: 57, Text: "Static noise"},
	}
	result := removeIsolatedArtifacts(cues)
	if len(result) != 0 {
		t.Errorf("expected all repeated artifacts removed, got %d cues", len(result))
	}
}

func TestRemoveIsolatedArtifacts_PreservesCloseRepeats(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Static noise"},
		{Index: 2, Start: 15, End: 17, Text: "Static noise"},
		{Index: 3, Start: 20, End: 22, Text: "Static noise"},
	}
	result := removeIsolatedArtifacts(cues)
	if len(result) != 3 {
		t.Errorf("expected 3 cues preserved (close together), got %d", len(result))
	}
}

func TestRemoveIsolatedArtifacts_MusicPattern(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Normal"},
		{Index: 2, Start: 50, End: 52, Text: "\u266A \u266B"},
		{Index: 3, Start: 90, End: 92, Text: "More"},
	}
	result := removeIsolatedArtifacts(cues)
	if len(result) != 2 {
		t.Errorf("expected isolated music cue removed, got %d cues", len(result))
	}
}

func TestSweepTrailingArtifacts_RemovesInLast300s(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 100, End: 102, Text: "Dialogue"},
		{Index: 2, Start: 500, End: 502, Text: "More dialogue"},
		{Index: 3, Start: 550, End: 552, Text: "\u266A"},
	}
	result := sweepTrailingArtifacts(cues, 600)
	if len(result) != 2 {
		t.Errorf("expected trailing artifacts removed, got %d cues", len(result))
	}
}

func TestSweepTrailingArtifacts_ShortVideoSkipped(t *testing.T) {
	cues := []srtutil.Cue{{Index: 1, Start: 10, End: 12, Text: "\u266A"}}
	result := sweepTrailingArtifacts(cues, 300)
	if len(result) != 1 {
		t.Errorf("expected no filtering for short video, got %d cues", len(result))
	}
}

func TestFilterCanonicalTranscriptOutput_FullPipeline(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:03,000
Hello world

2
00:00:05,000 --> 00:00:07,000
How are you

3
00:10:00,000 --> 00:10:02,000
♪

4
00:20:00,000 --> 00:20:02,000
Goodbye
`
	result, err := filterCanonicalTranscriptOutput(srt, 1200)
	if err != nil {
		t.Fatalf("filterCanonicalTranscriptOutput: %v", err)
	}

	cues := srtutil.Parse(result)
	if len(cues) != 3 {
		t.Fatalf("expected 3 cues after filtering, got %d", len(cues))
	}
	if cues[0].Index != 1 || cues[1].Index != 2 || cues[2].Index != 3 {
		t.Error("cues not renumbered correctly")
	}
}

func TestFilterCanonicalTranscriptOutput_AllRemoved(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:02,000
Noise

2
00:01:00,000 --> 00:01:01,000
Noise

3
00:02:00,000 --> 00:02:01,000
Noise
`
	_, err := filterCanonicalTranscriptOutput(srt, 300)
	if err == nil {
		t.Fatal("expected error when all cues removed")
	}
	if !strings.Contains(err.Error(), "all cues removed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFilterCanonicalTranscriptOutput_EmptyInput(t *testing.T) {
	_, err := filterCanonicalTranscriptOutput("", 100)
	if err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestRemoveIsolatedArtifacts_Boundaries(t *testing.T) {
	tests := []struct {
		name     string
		cues     []srtutil.Cue
		wantLen  int
		wantText []string
	}{
		{
			name: "rule1_gap_exactly_10s_preserved",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Noise"},
				{Index: 2, Start: 22, End: 24, Text: "Noise"},
				{Index: 3, Start: 34, End: 36, Text: "Noise"},
			},
			wantLen:  3,
			wantText: []string{"Noise", "Noise", "Noise"},
		},
		{
			name: "rule1_gap_just_over_10s_removed",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Noise"},
				{Index: 2, Start: 22.001, End: 24, Text: "Noise"},
				{Index: 3, Start: 34.002, End: 36, Text: "Noise"},
			},
			wantLen: 0,
		},
		{
			name: "music_gap_exactly_30s_removed",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 42, End: 44, Text: "\u266A"},
				{Index: 3, Start: 74, End: 76, Text: "More"},
			},
			wantLen:  2,
			wantText: []string{"Normal", "More"},
		},
		{
			name: "music_gap_just_under_30s_preserved",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 41.999, End: 44, Text: "\u266A"},
				{Index: 3, Start: 74, End: 76, Text: "More"},
			},
			wantLen:  3,
			wantText: []string{"Normal", "\u266A", "More"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeIsolatedArtifacts(tt.cues)
			if len(result) != tt.wantLen {
				t.Fatalf("got %d cues, want %d", len(result), tt.wantLen)
			}
			for i, want := range tt.wantText {
				if result[i].Text != want {
					t.Errorf("cue %d text = %q, want %q", i, result[i].Text, want)
				}
			}
		})
	}
}

func TestRemoveIsolatedArtifacts_RuleInteraction(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0, End: 2, Text: "Dialogue one"},
		{Index: 2, Start: 15, End: 17, Text: "Noise"},
		{Index: 3, Start: 30, End: 32, Text: "Noise"},
		{Index: 4, Start: 45, End: 47, Text: "Noise"},
		{Index: 5, Start: 100, End: 102, Text: "Dialogue two"},
		{Index: 6, Start: 140, End: 142, Text: "\u266A"},
		{Index: 7, Start: 200, End: 202, Text: "Dialogue three"},
	}

	result := removeIsolatedArtifacts(cues)
	if len(result) != 3 {
		t.Fatalf("got %d cues, want 3", len(result))
	}
	want := []string{"Dialogue one", "Dialogue two", "Dialogue three"}
	for i, w := range want {
		if result[i].Text != w {
			t.Errorf("cue %d text = %q, want %q", i, result[i].Text, w)
		}
	}
}

func TestRemoveIsolatedArtifacts_MultiLineCueRepeated(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Line one\nline two"},
		{Index: 2, Start: 25, End: 27, Text: "Line one\nline two"},
		{Index: 3, Start: 40, End: 42, Text: "Line one\nline two"},
	}
	result := removeIsolatedArtifacts(cues)
	if len(result) != 0 {
		t.Fatalf("expected repeated multiline cues removed, got %d", len(result))
	}
}
