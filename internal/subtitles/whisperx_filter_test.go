package subtitles

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Isolated/repeated hallucination tests ---

func TestRemoveIsolatedHallucination(t *testing.T) {
	cues := []srtCue{
		{index: 1, start: 10, end: 12, text: "Hello there."},
		{index: 2, start: 14, end: 16, text: "General Kenobi."},
		// Isolated "Thank you." with 60s+ gaps on both sides.
		{index: 3, start: 80, end: 82, text: "Thank you."},
		{index: 4, start: 150, end: 152, text: "Let's go."},
	}

	result := filterWhisperXOutput(cues, 200)

	if len(result.removals) != 1 {
		t.Fatalf("expected 1 removal, got %d", len(result.removals))
	}
	if result.removals[0].reason != "isolated_hallucination" {
		t.Errorf("expected reason isolated_hallucination, got %q", result.removals[0].reason)
	}
	if len(result.cues) != 3 {
		t.Fatalf("expected 3 remaining cues, got %d", len(result.cues))
	}

	// Same phrase mid-dialogue (2s gaps) should be preserved.
	midDialogue := []srtCue{
		{index: 1, start: 10, end: 12, text: "Hello there."},
		{index: 2, start: 14, end: 16, text: "Thank you."},
		{index: 3, start: 18, end: 20, text: "You're welcome."},
	}

	result2 := filterWhisperXOutput(midDialogue, 200)
	if len(result2.removals) != 0 {
		t.Errorf("expected 0 removals for mid-dialogue Thank you, got %d", len(result2.removals))
	}
}

func TestRemoveRepeatedHallucination(t *testing.T) {
	cues := []srtCue{
		{index: 1, start: 10, end: 12, text: "Real dialogue here."},
		// Five "Thank you." cues 15s apart with no other dialogue.
		{index: 2, start: 50, end: 52, text: "Thank you."},
		{index: 3, start: 67, end: 69, text: "Thank you."},
		{index: 4, start: 84, end: 86, text: "Thank you."},
		{index: 5, start: 101, end: 103, text: "Thank you."},
		{index: 6, start: 118, end: 120, text: "Thank you."},
	}

	result := filterWhisperXOutput(cues, 200)

	if len(result.removals) != 5 {
		t.Fatalf("expected 5 removals, got %d", len(result.removals))
	}
	for _, r := range result.removals {
		if r.reason != "repeated_hallucination" {
			t.Errorf("expected reason repeated_hallucination, got %q", r.reason)
		}
	}
	if len(result.cues) != 1 {
		t.Fatalf("expected 1 remaining cue, got %d", len(result.cues))
	}
	if result.cues[0].text != "Real dialogue here." {
		t.Errorf("expected real dialogue preserved, got %q", result.cues[0].text)
	}
}

func TestRemoveMusicSymbols(t *testing.T) {
	cues := []srtCue{
		{index: 1, start: 10, end: 12, text: "Hello there."},
		// Music-only cue with 40s gaps → should be removed.
		{index: 2, start: 55, end: 57, text: "\u00B6\u00B6"},
		{index: 3, start: 100, end: 102, text: "More dialogue."},
	}

	result := filterWhisperXOutput(cues, 200)

	if len(result.removals) != 1 {
		t.Fatalf("expected 1 removal, got %d", len(result.removals))
	}
	if result.removals[0].reason != "music_symbols" {
		t.Errorf("expected reason music_symbols, got %q", result.removals[0].reason)
	}

	// Mixed content (music + text) should be preserved.
	mixed := []srtCue{
		{index: 1, start: 10, end: 12, text: "Hello there."},
		{index: 2, start: 55, end: 57, text: "\u00B6\u00B6 La la la"},
		{index: 3, start: 100, end: 102, text: "More dialogue."},
	}

	result2 := filterWhisperXOutput(mixed, 200)
	if len(result2.removals) != 0 {
		t.Errorf("expected 0 removals for mixed music+text, got %d", len(result2.removals))
	}
}

func TestPreserveNormalDialogue(t *testing.T) {
	cues := []srtCue{
		{index: 1, start: 10, end: 13, text: "Hello there."},
		{index: 2, start: 15, end: 18, text: "General Kenobi, you are a bold one."},
		{index: 3, start: 20, end: 23, text: "Your move."},
		{index: 4, start: 25, end: 28, text: "You fool!"},
		{index: 5, start: 30, end: 33, text: "I have been trained in your Jedi arts."},
	}

	result := filterWhisperXOutput(cues, 200)

	if len(result.removals) != 0 {
		t.Errorf("expected 0 removals for clean dialogue, got %d", len(result.removals))
	}
	if len(result.cues) != 5 {
		t.Errorf("expected all 5 cues preserved, got %d", len(result.cues))
	}
}

// --- Trailing sweep tests ---

func TestSweepTrailingHallucinations(t *testing.T) {
	// Simulates Scream-like credits: hallucination phrases and music symbols
	// clustered in the last few minutes, too close together for isolation checks.
	videoSeconds := 6600.0

	cues := []srtCue{
		{index: 1, start: 6000, end: 6003, text: "Real dialogue near the end."},
		{index: 2, start: 6010, end: 6013, text: "More real dialogue."},
		// Credits noise — all within last 5 min (after 6300s)
		{index: 3, start: 6310, end: 6312, text: "Thank you."},
		{index: 4, start: 6325, end: 6335, text: "Song lyrics from credits music."},
		{index: 5, start: 6340, end: 6350, text: "\u00B6\u00B6"},
		{index: 6, start: 6360, end: 6362, text: "Thank you."},
		{index: 7, start: 6380, end: 6390, text: "More song lyrics here."},
		{index: 8, start: 6550, end: 6560, text: "We'll be right back."},
		{index: 9, start: 6570, end: 6575, text: "Thanks for watching."},
	}

	result := filterWhisperXOutput(cues, videoSeconds)

	// Should remove hallucination phrases and music symbols in trailing section.
	removed := make(map[string]int)
	for _, r := range result.removals {
		removed[r.reason]++
	}
	if removed["trailing_hallucination"] == 0 {
		t.Error("expected trailing_hallucination removals")
	}
	if removed["trailing_music"] == 0 {
		t.Error("expected trailing_music removals")
	}

	// Real dialogue and song lyrics should be preserved.
	for _, cue := range result.cues {
		norm := normalizeText(cue.text)
		if whisperXHallucinationPhrases[norm] {
			t.Errorf("hallucination phrase should have been removed: %q", cue.text)
		}
		if isWhisperMusicCue(cue.text) {
			t.Errorf("music cue should have been removed: %q", cue.text)
		}
	}

	// Song lyrics (not hallucination phrases) should remain.
	hasLyrics := false
	for _, cue := range result.cues {
		if cue.text == "Song lyrics from credits music." || cue.text == "More song lyrics here." {
			hasLyrics = true
		}
	}
	if !hasLyrics {
		t.Error("song lyrics should be preserved (not hallucination phrases)")
	}
}

func TestSweepTrailingPreservesDialogueOutsideWindow(t *testing.T) {
	// "Thank you." outside the trailing window (and mid-dialogue) should NOT
	// be removed. Only the one in the last 5 minutes should be swept.
	videoSeconds := 6600.0

	cues := []srtCue{
		{index: 1, start: 98, end: 100, text: "Here you go."},
		{index: 2, start: 102, end: 105, text: "Thank you."},      // mid-dialogue — preserved
		{index: 3, start: 108, end: 111, text: "You're welcome."}, // mid-dialogue
		{index: 4, start: 6350, end: 6353, text: "Thank you."},    // within last 5 min — removed
	}

	result := filterWhisperXOutput(cues, videoSeconds)

	if len(result.cues) != 3 {
		t.Fatalf("expected 3 remaining cues, got %d", len(result.cues))
	}
	found := false
	for _, cue := range result.cues {
		if cue.text == "Thank you." && cue.start < 200 {
			found = true
		}
	}
	if !found {
		t.Error("mid-dialogue 'Thank you.' should be preserved")
	}
}

func TestSweepTrailingSkipsShortVideos(t *testing.T) {
	// Short video (under 10 min) — trailing sweep should not fire.
	cues := []srtCue{
		{index: 1, start: 10, end: 12, text: "Hello."},
		{index: 2, start: 280, end: 282, text: "Thank you."},
	}

	result := filterWhisperXOutput(cues, 300)

	// "Thank you." at 280s is in the last 5 min of a 5-min video,
	// but trailing sweep should be skipped entirely for short content.
	trailingRemovals := 0
	for _, r := range result.removals {
		if r.reason == "trailing_hallucination" || r.reason == "trailing_music" {
			trailingRemovals++
		}
	}
	if trailingRemovals != 0 {
		t.Errorf("expected no trailing sweep for short video, got %d removals", trailingRemovals)
	}
}

// --- Orchestrator tests ---

func TestFilterCombinesBothPasses(t *testing.T) {
	// Dense dialogue with an isolated hallucination mid-file and
	// trailing hallucinations at the end.
	videoSeconds := 7000.0

	var cues []srtCue
	idx := 1
	// Dense dialogue for ~90 minutes.
	for sec := 0.0; sec < 5400; sec += 25 {
		cues = append(cues, srtCue{index: idx, start: sec, end: sec + 3, text: "Normal dialogue."})
		idx++
	}

	// Insert an isolated hallucination mid-dialogue (30s+ gaps both sides).
	insertAt := len(cues) / 2
	gapStart := cues[insertAt-1].end
	hallucinationCue := srtCue{index: idx, start: gapStart + 35, end: gapStart + 37, text: "Thanks for watching."}
	idx++
	shifted := make([]srtCue, 0, len(cues)+1)
	shifted = append(shifted, cues[:insertAt]...)
	shifted = append(shifted, hallucinationCue)
	for _, c := range cues[insertAt:] {
		c.start += 70
		c.end += 70
		c.index = idx
		idx++
		shifted = append(shifted, c)
	}

	// Add trailing hallucinations in the last 5 minutes.
	// Spaced 10s apart (8s gaps after 2s duration) so they are NOT isolated
	// (gaps < 30s) and NOT a repeated run (gaps ≤ 10s). Only the trailing
	// sweep should catch them.
	for i := 0; i < 3; i++ {
		sec := videoSeconds - 200 + float64(i)*10
		shifted = append(shifted, srtCue{index: idx, start: sec, end: sec + 2, text: "Bye."})
		idx++
	}

	result := filterWhisperXOutput(shifted, videoSeconds)

	// Should have both isolated and trailing removals.
	hasIsolated := false
	hasTrailing := false
	for _, r := range result.removals {
		switch r.reason {
		case "isolated_hallucination":
			hasIsolated = true
		case "trailing_hallucination":
			hasTrailing = true
		}
	}
	if !hasIsolated {
		t.Error("expected isolated_hallucination removals")
	}
	if !hasTrailing {
		t.Error("expected trailing_hallucination removals")
	}

	// Verify renumbering is sequential.
	for i, cue := range result.cues {
		if cue.index != i+1 {
			t.Errorf("cue %d has index %d, expected %d", i, cue.index, i+1)
			break
		}
	}
}

func TestFilterNoopOnCleanInput(t *testing.T) {
	cues := []srtCue{
		{index: 1, start: 10, end: 13, text: "Hello there."},
		{index: 2, start: 15, end: 18, text: "General Kenobi."},
		{index: 3, start: 20, end: 23, text: "Your move."},
		{index: 4, start: 25, end: 28, text: "You fool!"},
		{index: 5, start: 30, end: 33, text: "Attack!"},
	}

	result := filterWhisperXOutput(cues, 200)

	if len(result.removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(result.removals))
	}
	if len(result.cues) != len(cues) {
		t.Errorf("expected %d cues, got %d", len(cues), len(result.cues))
	}
	for i, cue := range result.cues {
		if cue.text != cues[i].text {
			t.Errorf("cue %d text changed: %q -> %q", i, cues[i].text, cue.text)
		}
	}
}

// --- Integration test via file ---

func TestFilterTranscriptionOutputIntegration(t *testing.T) {
	srtContent := `1
00:00:10,000 --> 00:00:13,000
Hello there.

2
00:00:15,000 --> 00:00:18,000
General Kenobi.

3
00:01:20,000 --> 00:01:22,000
Thank you.

4
00:02:30,000 --> 00:02:33,000
The end.
`
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(srtPath, []byte(srtContent), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &Service{}
	if _, err := svc.filterTranscriptionOutput(srtPath, 200); err != nil {
		t.Fatal(err)
	}

	// Read back and verify the isolated "Thank you." was removed.
	cues, err := parseSRTCues(srtPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 3 {
		t.Fatalf("expected 3 cues after filtering, got %d", len(cues))
	}
	for _, cue := range cues {
		if normalizeText(cue.text) == "thank you" {
			t.Error("isolated 'Thank you.' should have been removed")
		}
	}
	// Verify renumbering.
	for i, cue := range cues {
		if cue.index != i+1 {
			t.Errorf("cue %d has index %d, expected %d", i, cue.index, i+1)
		}
	}
}

// --- isWhisperMusicCue tests ---

func TestIsWhisperMusicCue(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"\u00B6\u00B6", true},
		{"\u266A \u266A", true},
		{"\u266B\u266B\u266B", true},
		{"* * *", true},
		{"\u00B6 \u266A *", true},
		{"", false},                      // empty
		{"   ", false},                   // whitespace only
		{"\u00B6\u00B6 La la la", false}, // mixed
		{"Hello", false},
	}
	for _, tt := range tests {
		got := isWhisperMusicCue(tt.text)
		if got != tt.want {
			t.Errorf("isWhisperMusicCue(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
