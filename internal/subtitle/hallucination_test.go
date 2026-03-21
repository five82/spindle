package subtitle

import (
	"strings"
	"testing"
)

func TestParseSRT_Basic(t *testing.T) {
	content := `1
00:00:01,000 --> 00:00:03,000
Hello world

2
00:00:05,000 --> 00:00:07,000
Second cue
`
	cues := parseSRT(content)
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
	cues := []srtCue{
		{Index: 1, Start: 1.0, End: 3.0, Text: "Hello"},
		{Index: 2, Start: 5.5, End: 7.5, Text: "World"},
	}
	output := formatSRT(cues)
	reparsed := parseSRT(output)
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
	cues := []srtCue{
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
	cues := []srtCue{
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
	cues := []srtCue{
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
	cues := []srtCue{
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
	cues := []srtCue{
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
	cues := []srtCue{
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
	cues := []srtCue{
		{Index: 1, Start: 10, End: 12, Text: "Thank you"},
	}
	result := sweepTrailingHallucinations(cues, 300)
	if len(result) != 1 {
		t.Errorf("expected no filtering for short video, got %d cues", len(result))
	}
}

func TestFilterWhisperXOutput_FullPipeline(t *testing.T) {
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
	result, err := filterWhisperXOutput(srt, 1200)
	if err != nil {
		t.Fatalf("filterWhisperXOutput: %v", err)
	}

	cues := parseSRT(result)
	if len(cues) != 3 {
		t.Fatalf("expected 3 cues after filtering, got %d", len(cues))
	}

	// Verify renumbering.
	if cues[0].Index != 1 || cues[1].Index != 2 || cues[2].Index != 3 {
		t.Error("cues not renumbered correctly")
	}
}

func TestFilterWhisperXOutput_AllRemoved(t *testing.T) {
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
	_, err := filterWhisperXOutput(srt, 300)
	if err == nil {
		t.Fatal("expected error when all cues removed")
	}
	if !strings.Contains(err.Error(), "all cues removed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFilterWhisperXOutput_EmptyInput(t *testing.T) {
	_, err := filterWhisperXOutput("", 100)
	if err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestParseTimestamp(t *testing.T) {
	got := parseTimestamp("01", "23", "45", "678")
	want := 1*3600 + 23*60 + 45 + 0.678
	if got != want {
		t.Errorf("parseTimestamp = %f, want %f", got, want)
	}
}

func TestFormatTimestamp(t *testing.T) {
	got := formatTimestamp(3723.456)
	want := "01:02:03,456"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}
