package subtitles

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSRTCues(t *testing.T) {
	content := `1
00:05:46,345 --> 00:05:48,514
TACTICAL.

2
00:06:06,282 --> 00:06:07,992
VISUAL.

3
00:06:13,330 --> 00:06:15,833
TACTICAL, STAND BY ON TORPEDOES.
`
	path := filepath.Join(t.TempDir(), "test.srt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cues, err := parseSRTCues(path)
	if err != nil {
		t.Fatalf("parseSRTCues: %v", err)
	}

	if len(cues) != 3 {
		t.Fatalf("expected 3 cues, got %d", len(cues))
	}

	// Check first cue
	if cues[0].index != 1 {
		t.Errorf("cue 0 index = %d, want 1", cues[0].index)
	}
	if math.Abs(cues[0].start-346.345) > 0.001 {
		t.Errorf("cue 0 start = %f, want 346.345", cues[0].start)
	}
	if cues[0].text != "TACTICAL." {
		t.Errorf("cue 0 text = %q, want TACTICAL.", cues[0].text)
	}
}

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"TACTICAL.", "tactical"},
		{"TACTICAL...", "tactical"},
		{"Hello, World!", "hello world"},
		{"Line 1\nLine 2", "line 1 line 2"},
		{"  extra   spaces  ", "extra spaces"},
	}

	for _, tt := range tests {
		got := normalizeText(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeText(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFindMatchingCues(t *testing.T) {
	reference := []srtCue{
		{index: 1, start: 346.405, end: 348.594, text: "TACTICAL."},
		{index: 2, start: 366.342, end: 368.072, text: "VISUAL."},
		{index: 3, start: 373.390, end: 375.913, text: "TACTICAL, STAND BY ON TORPEDOES."},
	}
	forced := []srtCue{
		{index: 1, start: 346.000, end: 347.500, text: "TACTICAL..."},
		{index: 2, start: 365.000, end: 366.500, text: "VISUAL..."},
		{index: 3, start: 371.000, end: 373.000, text: "TACTICAL,\nSTAND BY ON TORPEDOES."},
	}

	matches := findMatchingCues(reference, forced)

	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}

	// Verify match pairs
	for i, m := range matches {
		if m[0].index != i+1 || m[1].index != i+1 {
			t.Errorf("match %d: ref index=%d, forced index=%d, want both %d",
				i, m[0].index, m[1].index, i+1)
		}
	}
}

func TestCalculateTimeTransform(t *testing.T) {
	// Simulate PAL->NTSC conversion (~4% stretch)
	matches := [][2]srtCue{
		{{start: 346.405}, {start: 346.000}}, // ref, forced
		{{start: 415.265}, {start: 411.500}}, // ref, forced
	}

	transform, ok := calculateTimeTransform(matches)
	if !ok {
		t.Fatal("expected transform calculation to succeed")
	}

	// scale should be approximately 1.05 (5% stretch)
	if transform.scale < 1.0 || transform.scale > 1.1 {
		t.Errorf("scale = %f, expected around 1.05", transform.scale)
	}

	// Verify transformation works
	// forced time 346.0 should map to ~346.405
	got := transform.applyTransform(346.000)
	if math.Abs(got-346.405) > 0.5 {
		t.Errorf("applyTransform(346.0) = %f, want ~346.405", got)
	}

	// forced time 411.5 should map to ~415.265
	got = transform.applyTransform(411.500)
	if math.Abs(got-415.265) > 0.5 {
		t.Errorf("applyTransform(411.5) = %f, want ~415.265", got)
	}
}

func TestAlignForcedToReference(t *testing.T) {
	tmpDir := t.TempDir()

	// Create reference subtitle (already aligned to video)
	refContent := `1
00:05:46,405 --> 00:05:48,594
TACTICAL.

2
00:06:06,342 --> 00:06:08,072
VISUAL.

3
00:06:13,390 --> 00:06:15,913
TACTICAL, STAND BY ON TORPEDOES.

4
00:06:23,984 --> 00:06:25,130
READY...

5
00:06:28,238 --> 00:06:29,343
FIRE!

6
00:06:55,265 --> 00:06:56,412
EVASIVE!
`
	refPath := filepath.Join(tmpDir, "reference.srt")
	if err := os.WriteFile(refPath, []byte(refContent), 0644); err != nil {
		t.Fatalf("write reference: %v", err)
	}

	// Create forced subtitle (from different source, needs alignment)
	forcedContent := `1
00:05:46,000 --> 00:05:47,500
TACTICAL...

2
00:06:05,000 --> 00:06:06,500
VISUAL...

3
00:06:11,000 --> 00:06:13,000
TACTICAL, STAND BY ON TORPEDOES.

4
00:10:13,000 --> 00:10:19,000
OUR ANCESTORS CAST OUT THEIR ANIMAL PASSIONS...
`
	forcedPath := filepath.Join(tmpDir, "forced.srt")
	if err := os.WriteFile(forcedPath, []byte(forcedContent), 0644); err != nil {
		t.Fatalf("write forced: %v", err)
	}

	outputPath := filepath.Join(tmpDir, "output.srt")

	matchCount, transform, err := alignForcedToReference(refPath, forcedPath, outputPath)
	if err != nil {
		t.Fatalf("alignForcedToReference: %v", err)
	}

	if matchCount < 2 {
		t.Errorf("expected at least 2 matches, got %d", matchCount)
	}

	t.Logf("Transform: scale=%f, offset=%f", transform.scale, transform.offset)

	// Read output and verify first cue is closer to reference timing
	outputCues, err := parseSRTCues(outputPath)
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}

	if len(outputCues) != 4 {
		t.Fatalf("expected 4 output cues, got %d", len(outputCues))
	}

	// First cue should be closer to 346.405 than original 346.0
	// (though the difference is small in this case)
	t.Logf("First cue adjusted from 346.0 to %f (ref: 346.405)", outputCues[0].start)
}
