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
		{"SUBSCRIBE!!!", "subscribe"},
		{"you.", "you"},
	}
	for _, tt := range tests {
		got := normalizeText(tt.input)
		if got != tt.want {
			t.Errorf("normalizeText(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRemoveIsolatedHallucinations_RepeatedPhrase(t *testing.T) {
	// 4 consecutive "thank you" cues with >10s gaps between each.
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Thank you"},
		{Index: 2, Start: 25, End: 27, Text: "Thank you"},
		{Index: 3, Start: 40, End: 42, Text: "Thank you"},
		{Index: 4, Start: 55, End: 57, Text: "Thank you"},
	}
	result := removeIsolatedHallucinations(cues)
	if len(result) != 0 {
		t.Errorf("expected all repeated hallucinations removed, got %d cues", len(result))
	}
}

func TestRemoveIsolatedHallucinations_PreservesCloseRepeats(t *testing.T) {
	// Repeated text but gaps <= 10s: should be preserved.
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Thank you"},
		{Index: 2, Start: 15, End: 17, Text: "Thank you"},
		{Index: 3, Start: 20, End: 22, Text: "Thank you"},
	}
	result := removeIsolatedHallucinations(cues)
	if len(result) != 3 {
		t.Errorf("expected 3 cues preserved (close together), got %d", len(result))
	}
}

func TestRemoveIsolatedHallucinations_KnownPhrase(t *testing.T) {
	// Isolated known phrase with >= 30s gaps on both sides.
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Normal dialogue"},
		{Index: 2, Start: 50, End: 52, Text: "Thank you"},
		{Index: 3, Start: 90, End: 92, Text: "More dialogue"},
	}
	result := removeIsolatedHallucinations(cues)
	if len(result) != 2 {
		t.Errorf("expected isolated hallucination removed, got %d cues", len(result))
	}
	if result[0].Text != "Normal dialogue" || result[1].Text != "More dialogue" {
		t.Error("wrong cues preserved")
	}
}

func TestRemoveIsolatedHallucinations_PreservesNonIsolated(t *testing.T) {
	// Known phrase but gaps < 30s: should be preserved.
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Normal"},
		{Index: 2, Start: 20, End: 22, Text: "Thank you"},
		{Index: 3, Start: 30, End: 32, Text: "More"},
	}
	result := removeIsolatedHallucinations(cues)
	if len(result) != 3 {
		t.Errorf("expected all cues preserved (not isolated), got %d", len(result))
	}
}

func TestRemoveIsolatedHallucinations_MusicPattern(t *testing.T) {
	// Isolated music symbol cue.
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Normal"},
		{Index: 2, Start: 50, End: 52, Text: "\u266A \u266B"},
		{Index: 3, Start: 90, End: 92, Text: "More"},
	}
	result := removeIsolatedHallucinations(cues)
	if len(result) != 2 {
		t.Errorf("expected isolated music cue removed, got %d cues", len(result))
	}
}

func TestSweepTrailingHallucinations_RemovesInLast300s(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 100, End: 102, Text: "Dialogue"},
		{Index: 2, Start: 500, End: 502, Text: "More dialogue"},
		{Index: 3, Start: 550, End: 552, Text: "Thank you"},
		{Index: 4, Start: 580, End: 582, Text: "\u266A"},
	}
	// Video is 600s, threshold = 300s.
	result := sweepTrailingHallucinations(cues, 600)
	if len(result) != 2 {
		t.Errorf("expected trailing hallucinations removed, got %d cues", len(result))
	}
}

func TestSweepTrailingHallucinations_ShortVideoSkipped(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 10, End: 12, Text: "Thank you"},
	}
	result := sweepTrailingHallucinations(cues, 300)
	if len(result) != 1 {
		t.Errorf("expected no filtering for short video, got %d cues", len(result))
	}
}

func TestFilterTranscriptionOutput_FullPipeline(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:03,000
Hello world

2
00:00:05,000 --> 00:00:07,000
How are you

3
00:10:00,000 --> 00:10:02,000
Thank you

4
00:20:00,000 --> 00:20:02,000
Goodbye
`
	// Video is 1200s. Cue 3 is isolated (gaps > 30s) and known phrase.
	result, err := filterTranscriptionOutput(srt, 1200)
	if err != nil {
		t.Fatalf("filterTranscriptionOutput: %v", err)
	}

	cues := srtutil.Parse(result)
	if len(cues) != 3 {
		t.Fatalf("expected 3 cues after filtering, got %d", len(cues))
	}

	// Verify renumbering.
	if cues[0].Index != 1 || cues[1].Index != 2 || cues[2].Index != 3 {
		t.Error("cues not renumbered correctly")
	}
}

func TestFilterTranscriptionOutput_AllRemoved(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:02,000
Thank you

2
00:01:00,000 --> 00:01:01,000
Thank you

3
00:02:00,000 --> 00:02:01,000
Thank you
`
	_, err := filterTranscriptionOutput(srt, 300)
	if err == nil {
		t.Fatal("expected error when all cues removed")
	}
	if !strings.Contains(err.Error(), "all cues removed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFilterTranscriptionOutput_EmptyInput(t *testing.T) {
	_, err := filterTranscriptionOutput("", 100)
	if err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestRemoveIsolatedHallucinations_Boundaries(t *testing.T) {
	tests := []struct {
		name     string
		cues     []srtutil.Cue
		wantLen  int
		wantText []string // expected surviving cue texts
	}{
		{
			name: "rule1_gap_exactly_10s_preserved",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Thank you"},
				{Index: 2, Start: 22, End: 24, Text: "Thank you"}, // gap = 10.0
				{Index: 3, Start: 34, End: 36, Text: "Thank you"}, // gap = 10.0
			},
			wantLen:  3,
			wantText: []string{"Thank you", "Thank you", "Thank you"},
		},
		{
			name: "rule1_gap_just_over_10s_removed",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Thank you"},
				{Index: 2, Start: 22.001, End: 24, Text: "Thank you"}, // gap = 10.001
				{Index: 3, Start: 34.002, End: 36, Text: "Thank you"}, // gap = 10.002
			},
			wantLen: 0,
		},
		{
			name: "rule2_gap_exactly_30s_removed",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 42, End: 44, Text: "Thank you"}, // gap before = 30.0
				{Index: 3, Start: 74, End: 76, Text: "More"},      // gap after = 30.0
			},
			wantLen:  2,
			wantText: []string{"Normal", "More"},
		},
		{
			name: "rule2_gap_just_under_30s_preserved",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 41.999, End: 44, Text: "Thank you"}, // gap before = 29.999
				{Index: 3, Start: 74, End: 76, Text: "More"},
			},
			wantLen:  3,
			wantText: []string{"Normal", "Thank you", "More"},
		},
		{
			name: "rule3_music_gap_exactly_30s_removed",
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 42, End: 44, Text: "\u266A"}, // gap before = 30.0
				{Index: 3, Start: 74, End: 76, Text: "More"},   // gap after = 30.0
			},
			wantLen:  2,
			wantText: []string{"Normal", "More"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeIsolatedHallucinations(tt.cues)
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

func TestRemoveIsolatedHallucinations_RuleInteraction(t *testing.T) {
	// Rule 1 removes repeated "thank you" cues, compaction changes gaps,
	// then Rule 2 removes "bye" which is now isolated by >= 30s.
	cues := []srtutil.Cue{
		{Index: 1, Start: 0, End: 2, Text: "Dialogue one"},
		{Index: 2, Start: 15, End: 17, Text: "Thank you"}, // repeated run start
		{Index: 3, Start: 30, End: 32, Text: "Thank you"}, // gap = 13
		{Index: 4, Start: 45, End: 47, Text: "Thank you"}, // gap = 13
		{Index: 5, Start: 100, End: 102, Text: "Dialogue two"},
		{Index: 6, Start: 140, End: 142, Text: "Bye"}, // gap before = 38, after = 58
		{Index: 7, Start: 200, End: 202, Text: "Dialogue three"},
	}

	result := removeIsolatedHallucinations(cues)

	// Rule 1 removes cues 2-4 (3 identical, gaps > 10s).
	// After compaction: [Dialogue one@0, Dialogue two@100, Bye@140, Dialogue three@200]
	// Rule 2 removes "Bye" (known phrase, gap before=38s >= 30, gap after=58s >= 30).
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

func TestRemoveIsolatedHallucinations_MultiLineCue(t *testing.T) {
	tests := []struct {
		name     string
		cues     []srtutil.Cue
		wantLen  int
		wantText []string
	}{
		{
			name: "multiline_known_phrase_not_matched",
			// "Thank you\nso much" normalizes to "thank you so much" - not in known phrases.
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 50, End: 52, Text: "Thank you\nso much"},
				{Index: 3, Start: 90, End: 92, Text: "More"},
			},
			wantLen:  3,
			wantText: []string{"Normal", "Thank you\nso much", "More"},
		},
		{
			name: "multiline_first_line_is_known_phrase",
			// Full normalized text "thank you for everything" is not in the map.
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 50, End: 52, Text: "Thank you\nfor everything"},
				{Index: 3, Start: 90, End: 92, Text: "More"},
			},
			wantLen:  3,
			wantText: []string{"Normal", "Thank you\nfor everything", "More"},
		},
		{
			name: "single_word_known_phrase_removed",
			// "You" normalizes to "you" which IS in known phrases.
			cues: []srtutil.Cue{
				{Index: 1, Start: 10, End: 12, Text: "Normal"},
				{Index: 2, Start: 50, End: 52, Text: "You"},
				{Index: 3, Start: 90, End: 92, Text: "More"},
			},
			wantLen:  2,
			wantText: []string{"Normal", "More"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeIsolatedHallucinations(tt.cues)
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
