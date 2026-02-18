package subtitles

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeSRT writes cues as an SRT file at the given path.
func writeSRT(t *testing.T, path string, cues []srtCue) {
	t.Helper()
	if err := writeSRTCues(path, cues); err != nil {
		t.Fatalf("write SRT %s: %v", path, err)
	}
}

// makeCues generates n cues starting at startSec with the given interval and duration.
func makeCues(n int, startSec, interval, duration float64) []srtCue {
	cues := make([]srtCue, n)
	for i := range n {
		s := startSec + float64(i)*interval
		cues[i] = srtCue{
			index: i + 1,
			start: s,
			end:   s + duration,
			text:  fmt.Sprintf("Cue %d", i+1),
		}
	}
	return cues
}

func TestAnalyzeAlignmentQuality_ConstantOffset(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 10.0, 5.0, 2.0)

	// Apply constant +3s offset to all cues
	after := make([]srtCue, len(before))
	for i, c := range before {
		after[i] = srtCue{index: c.index, start: c.start + 3.0, end: c.end + 3.0, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.cueCount != 100 {
		t.Errorf("cueCount = %d, want 100", m.cueCount)
	}
	if math.Abs(m.shiftMedian-3.0) > 0.01 {
		t.Errorf("median = %.3f, want 3.0", m.shiftMedian)
	}
	if m.shiftStdDev > 0.01 {
		t.Errorf("stddev = %.3f, want ~0", m.shiftStdDev)
	}
	if m.negativeTimestamps != 0 {
		t.Errorf("negativeTimestamps = %d, want 0", m.negativeTimestamps)
	}
	if m.zeroDurationCues != 0 {
		t.Errorf("zeroDurationCues = %d, want 0", m.zeroDurationCues)
	}
	if m.newOverlaps != 0 {
		t.Errorf("newOverlaps = %d, want 0", m.newOverlaps)
	}
}

func TestAnalyzeAlignmentQuality_LinearDrift(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 10.0, 5.0, 2.0)

	// Apply linearly increasing shift: 0s at start, 10s at end
	after := make([]srtCue, len(before))
	for i, c := range before {
		shift := float64(i) / float64(len(before)-1) * 10.0
		after[i] = srtCue{index: c.index, start: c.start + shift, end: c.end + shift, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Median should be around 5s (middle of 0-10 range)
	if math.Abs(m.shiftMedian-5.0) > 0.5 {
		t.Errorf("median = %.3f, want ~5.0", m.shiftMedian)
	}
	// Stddev should be moderate but coherent relative to median
	if m.shiftStdDev < 1.0 {
		t.Errorf("stddev = %.3f, expected > 1.0 for linear drift", m.shiftStdDev)
	}
}

func TestAnalyzeAlignmentQuality_Chaotic(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 10.0, 5.0, 2.0)

	// Apply chaotic shifts: alternating +50s and -50s
	after := make([]srtCue, len(before))
	for i, c := range before {
		shift := 50.0
		if i%2 == 0 {
			shift = -50.0
		}
		s := c.start + shift
		e := c.end + shift
		// Clamp to avoid negative timestamps (which would trigger a different rejection)
		if s < 0 {
			e -= s
			s = 0
		}
		after[i] = srtCue{index: c.index, start: s, end: e, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stddev should be very high due to chaotic shifts
	if m.shiftStdDev < 10.0 {
		t.Errorf("stddev = %.3f, expected > 10.0 for chaotic shifts", m.shiftStdDev)
	}
}

func TestAnalyzeAlignmentQuality_Identity(t *testing.T) {
	dir := t.TempDir()
	cues := makeCues(50, 10.0, 5.0, 2.0)

	path := filepath.Join(dir, "same.srt")
	writeSRT(t, path, cues)

	m, err := analyzeAlignmentQuality(path, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.shiftMedian != 0 {
		t.Errorf("median = %.3f, want 0", m.shiftMedian)
	}
	if m.shiftStdDev != 0 {
		t.Errorf("stddev = %.3f, want 0", m.shiftStdDev)
	}
	if m.shiftMax != 0 {
		t.Errorf("max = %.3f, want 0", m.shiftMax)
	}
}

func TestAnalyzeAlignmentQuality_NegativeTimestamps(t *testing.T) {
	// SRT format clamps negative timestamps to 00:00:00,000.
	// Write a raw SRT with cues at 0 that would indicate clamping from negative values.
	// In practice, negative timestamps can't be represented in SRT files, so this test
	// verifies that the analysis correctly detects cues forced to zero by formatSRTTimestamp.
	dir := t.TempDir()
	before := makeCues(10, 5.0, 5.0, 2.0)

	// After alignment: first cue has start and end both at 0 (clamped from negative)
	after := make([]srtCue, len(before))
	copy(after, before)
	after[0] = srtCue{index: 1, start: 0.0, end: 0.0, text: "Cue 1"} // end <= start

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The clamped cue has end <= start, so it appears as zero-duration
	if m.zeroDurationCues != 1 {
		t.Errorf("zeroDurationCues = %d, want 1", m.zeroDurationCues)
	}
}

func TestAnalyzeAlignmentQuality_ZeroDurationCues(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(10, 10.0, 5.0, 2.0)

	// Create cues where end <= start
	after := make([]srtCue, len(before))
	copy(after, before)
	after[0] = srtCue{index: 1, start: 10.0, end: 10.0, text: "Zero"} // end == start
	after[1] = srtCue{index: 2, start: 15.0, end: 14.5, text: "Neg"}  // end < start

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.zeroDurationCues != 2 {
		t.Errorf("zeroDurationCues = %d, want 2", m.zeroDurationCues)
	}
}

func TestAnalyzeAlignmentQuality_NewOverlaps(t *testing.T) {
	dir := t.TempDir()
	// Before: well-separated cues, no overlaps
	before := makeCues(10, 10.0, 5.0, 2.0) // 2s duration, 5s interval -> 3s gap

	// After: squeeze cues together to create overlaps
	after := make([]srtCue, len(before))
	for i, c := range before {
		after[i] = srtCue{
			index: c.index,
			start: 10.0 + float64(i)*1.5,       // 1.5s interval
			end:   10.0 + float64(i)*1.5 + 2.0, // 2s duration -> 0.5s overlap
			text:  c.text,
		}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All 9 consecutive pairs should overlap (10 cues -> 9 pairs)
	if m.newOverlaps != 9 {
		t.Errorf("newOverlaps = %d, want 9", m.newOverlaps)
	}
	if m.preExistingOverlaps != 0 {
		t.Errorf("preExistingOverlaps = %d, want 0", m.preExistingOverlaps)
	}
}

func TestAnalyzeAlignmentQuality_PreExistingOverlapsExcluded(t *testing.T) {
	dir := t.TempDir()
	// Before: 3 overlapping cues
	before := []srtCue{
		{index: 1, start: 10.0, end: 13.0, text: "A"},
		{index: 2, start: 12.0, end: 15.0, text: "B"}, // overlaps with A
		{index: 3, start: 14.0, end: 17.0, text: "C"}, // overlaps with B
		{index: 4, start: 20.0, end: 22.0, text: "D"},
		{index: 5, start: 25.0, end: 27.0, text: "E"},
	}

	// After: same overlaps maintained, no new ones
	after := make([]srtCue, len(before))
	for i, c := range before {
		after[i] = srtCue{index: c.index, start: c.start + 1.0, end: c.end + 1.0, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.preExistingOverlaps != 2 {
		t.Errorf("preExistingOverlaps = %d, want 2", m.preExistingOverlaps)
	}
	if m.newOverlaps != 0 {
		t.Errorf("newOverlaps = %d, want 0 (overlaps were pre-existing)", m.newOverlaps)
	}
}

func TestCheckAlignmentQuality_AcceptsConstantOffset(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 10.0, 5.0, 2.0)
	after := make([]srtCue, len(before))
	for i, c := range before {
		after[i] = srtCue{index: c.index, start: c.start + 5.0, end: c.end + 5.0, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	if err := checkAlignmentQuality(beforePath, afterPath, "test-release"); err != nil {
		t.Errorf("constant offset should be accepted, got: %v", err)
	}
}

func TestCheckAlignmentQuality_AcceptsIdentity(t *testing.T) {
	dir := t.TempDir()
	cues := makeCues(50, 10.0, 5.0, 2.0)
	path := filepath.Join(dir, "same.srt")
	writeSRT(t, path, cues)

	if err := checkAlignmentQuality(path, path, "test-release"); err != nil {
		t.Errorf("identity should be accepted, got: %v", err)
	}
}

func TestCheckAlignmentQuality_AcceptsLinearDrift(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 10.0, 5.0, 2.0)
	after := make([]srtCue, len(before))
	for i, c := range before {
		// Linear drift: 2s to 8s over 100 cues (coherent, moderate stddev/median ratio)
		shift := 2.0 + float64(i)/float64(len(before)-1)*6.0
		after[i] = srtCue{index: c.index, start: c.start + shift, end: c.end + shift, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	if err := checkAlignmentQuality(beforePath, afterPath, "test-release"); err != nil {
		t.Errorf("linear drift should be accepted, got: %v", err)
	}
}

func TestCheckAlignmentQuality_RejectsChaotic(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 100.0, 5.0, 2.0) // Start at 100s to avoid negative timestamps

	// Chaotic: alternating +30s / -30s
	after := make([]srtCue, len(before))
	for i, c := range before {
		shift := 30.0
		if i%2 == 0 {
			shift = -30.0
		}
		after[i] = srtCue{index: c.index, start: c.start + shift, end: c.end + shift, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	err := checkAlignmentQuality(beforePath, afterPath, "test-release")
	if err == nil {
		t.Fatal("chaotic alignment should be rejected")
	}
	t.Logf("rejected with: %s", err.reason)
}

func TestCheckAlignmentQuality_RejectsClampedToZero(t *testing.T) {
	// Negative timestamps get clamped to 0 by SRT formatting, resulting in zero-duration cues.
	dir := t.TempDir()
	before := makeCues(10, 5.0, 5.0, 2.0)
	after := make([]srtCue, len(before))
	copy(after, before)
	after[0] = srtCue{index: 1, start: 0.0, end: 0.0, text: "Clamped"}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	err := checkAlignmentQuality(beforePath, afterPath, "test-release")
	if err == nil {
		t.Fatal("zero-duration cue from clamping should be rejected")
	}
	if err.metrics.zeroDurationCues != 1 {
		t.Errorf("zeroDurationCues = %d, want 1", err.metrics.zeroDurationCues)
	}
}

func TestCheckAlignmentQuality_RejectsZeroDuration(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(10, 10.0, 5.0, 2.0)
	after := make([]srtCue, len(before))
	copy(after, before)
	after[0] = srtCue{index: 1, start: 10.0, end: 10.0, text: "Zero"}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	err := checkAlignmentQuality(beforePath, afterPath, "test-release")
	if err == nil {
		t.Fatal("zero-duration cues should be rejected")
	}
	if err.metrics.zeroDurationCues != 1 {
		t.Errorf("zeroDurationCues = %d, want 1", err.metrics.zeroDurationCues)
	}
}

func TestCheckAlignmentQuality_RejectsPartialFailure(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(100, 10.0, 5.0, 2.0)

	// Partial failure: most cues unchanged, some wildly shifted
	after := make([]srtCue, len(before))
	for i, c := range before {
		shift := 0.0
		if i%5 == 0 {
			shift = 15.0 // Every 5th cue is shifted by 15s
		}
		after[i] = srtCue{index: c.index, start: c.start + shift, end: c.end + shift, text: c.text}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	err := checkAlignmentQuality(beforePath, afterPath, "test-release")
	if err == nil {
		t.Fatal("partial alignment failure should be rejected")
	}
	t.Logf("rejected with: %s", err.reason)
}

func TestCheckAlignmentQuality_RejectsNewOverlaps(t *testing.T) {
	dir := t.TempDir()
	before := makeCues(20, 10.0, 5.0, 2.0)

	// Create alignment that introduces overlaps for >10% of cues
	after := make([]srtCue, len(before))
	for i, c := range before {
		after[i] = srtCue{
			index: c.index,
			start: 10.0 + float64(i)*1.5,
			end:   10.0 + float64(i)*1.5 + 2.0,
			text:  c.text,
		}
	}

	beforePath := filepath.Join(dir, "before.srt")
	afterPath := filepath.Join(dir, "after.srt")
	writeSRT(t, beforePath, before)
	writeSRT(t, afterPath, after)

	err := checkAlignmentQuality(beforePath, afterPath, "test-release")
	if err == nil {
		t.Fatal("excessive new overlaps should be rejected")
	}
	t.Logf("rejected with: %s", err.reason)
}

func TestCheckAlignmentQuality_EmptyFiles(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(emptyPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Empty files should not error - nothing to compare
	if err := checkAlignmentQuality(emptyPath, emptyPath, "test"); err != nil {
		t.Errorf("empty files should pass, got: %v", err)
	}
}

func TestCheckOutputIntegrity_Clean(t *testing.T) {
	dir := t.TempDir()
	cues := makeCues(50, 10.0, 5.0, 2.0)
	path := filepath.Join(dir, "clean.srt")
	writeSRT(t, path, cues)

	if err := checkOutputIntegrity(path, "test"); err != nil {
		t.Errorf("clean file should pass, got: %v", err)
	}
}

func TestCheckOutputIntegrity_ClampedTimestamps(t *testing.T) {
	// Negative timestamps get clamped to 0 by SRT formatting, creating zero-duration cues.
	dir := t.TempDir()
	cues := []srtCue{
		{index: 1, start: 0.0, end: 0.0, text: "Clamped from negative"},
		{index: 2, start: 5.0, end: 7.0, text: "OK"},
	}
	path := filepath.Join(dir, "clamped.srt")
	writeSRT(t, path, cues)

	err := checkOutputIntegrity(path, "test")
	if err == nil {
		t.Fatal("zero-duration cue from clamping should be rejected")
	}
	if err.metrics.zeroDurationCues != 1 {
		t.Errorf("zeroDurationCues = %d, want 1", err.metrics.zeroDurationCues)
	}
}

func TestCheckOutputIntegrity_ZeroDuration(t *testing.T) {
	dir := t.TempDir()
	cues := []srtCue{
		{index: 1, start: 5.0, end: 5.0, text: "Zero"},
		{index: 2, start: 10.0, end: 9.0, text: "Negative"},
		{index: 3, start: 15.0, end: 17.0, text: "OK"},
	}
	path := filepath.Join(dir, "zerodur.srt")
	writeSRT(t, path, cues)

	err := checkOutputIntegrity(path, "test")
	if err == nil {
		t.Fatal("zero-duration cues should be rejected")
	}
	if err.metrics.zeroDurationCues != 2 {
		t.Errorf("zeroDurationCues = %d, want 2", err.metrics.zeroDurationCues)
	}
}

func TestCheckOutputIntegrity_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	if err := checkOutputIntegrity(path, "test"); err != nil {
		t.Errorf("empty file should pass, got: %v", err)
	}
}

func TestCountOverlaps(t *testing.T) {
	tests := []struct {
		name     string
		cues     []srtCue
		expected int
	}{
		{
			name:     "no overlaps",
			cues:     makeCues(5, 10.0, 5.0, 2.0),
			expected: 0,
		},
		{
			name: "all overlapping",
			cues: []srtCue{
				{start: 0, end: 3},
				{start: 2, end: 5},
				{start: 4, end: 7},
			},
			expected: 2,
		},
		{
			name: "one overlap",
			cues: []srtCue{
				{start: 0, end: 3},
				{start: 2, end: 4},
				{start: 10, end: 12},
			},
			expected: 1,
		},
		{
			name:     "single cue",
			cues:     []srtCue{{start: 0, end: 1}},
			expected: 0,
		},
		{
			name:     "empty",
			cues:     nil,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countOverlaps(tt.cues)
			if got != tt.expected {
				t.Errorf("countOverlaps() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestAlignmentQualityError_Error(t *testing.T) {
	err := alignmentQualityError{reason: "test failure", release: "TestRelease"}
	expected := "alignment quality failed: test failure"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}
