package keyframe

import (
	"testing"
)

func TestGenerateFixedChunks(t *testing.T) {
	tests := []struct {
		name              string
		totalFrames       int
		fpsNum            uint32
		fpsDen            uint32
		chunkDurationSecs float64
		expected          []int
	}{
		{
			name:              "24fps 10 second chunks",
			totalFrames:       2400, // 100 seconds
			fpsNum:            24,
			fpsDen:            1,
			chunkDurationSecs: 10.0,
			expected:          []int{0, 240, 480, 720, 960, 1200, 1440, 1680, 1920, 2160},
		},
		{
			name:              "30fps 10 second chunks",
			totalFrames:       3000, // 100 seconds
			fpsNum:            30,
			fpsDen:            1,
			chunkDurationSecs: 10.0,
			expected:          []int{0, 300, 600, 900, 1200, 1500, 1800, 2100, 2400, 2700},
		},
		{
			name:              "60fps 20 second chunks",
			totalFrames:       6000, // 100 seconds
			fpsNum:            60,
			fpsDen:            1,
			chunkDurationSecs: 20.0,
			expected:          []int{0, 1200, 2400, 3600, 4800},
		},
		{
			name:              "23.976fps NTSC film 10 second chunks",
			totalFrames:       2398, // ~100 seconds
			fpsNum:            24000,
			fpsDen:            1001,
			chunkDurationSecs: 10.0,
			// ~23.976 * 10 = ~239 frames per chunk
			expected: []int{0, 239, 478, 717, 956, 1195, 1434, 1673, 1912, 2151, 2390},
		},
		{
			name:              "exact multiple",
			totalFrames:       720, // 30 seconds at 24fps
			fpsNum:            24,
			fpsDen:            1,
			chunkDurationSecs: 10.0,
			expected:          []int{0, 240, 480},
		},
		{
			name:              "short video single chunk",
			totalFrames:       100,
			fpsNum:            24,
			fpsDen:            1,
			chunkDurationSecs: 10.0,
			expected:          []int{0},
		},
		{
			name:              "zero denominator",
			totalFrames:       1000,
			fpsNum:            24,
			fpsDen:            0,
			chunkDurationSecs: 10.0,
			expected:          []int{0},
		},
		{
			name:              "zero total frames",
			totalFrames:       0,
			fpsNum:            24,
			fpsDen:            1,
			chunkDurationSecs: 10.0,
			expected:          []int{0},
		},
		{
			name:              "negative total frames",
			totalFrames:       -100,
			fpsNum:            24,
			fpsDen:            1,
			chunkDurationSecs: 10.0,
			expected:          []int{0},
		},
		{
			name:              "very short chunk duration",
			totalFrames:       100,
			fpsNum:            24,
			fpsDen:            1,
			chunkDurationSecs: 0.5,
			// 24 * 0.5 = 12 frames per chunk
			expected: []int{0, 12, 24, 36, 48, 60, 72, 84, 96},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateFixedChunks(tt.totalFrames, tt.fpsNum, tt.fpsDen, tt.chunkDurationSecs)
			if !intSliceEqual(result, tt.expected) {
				t.Errorf("GenerateFixedChunks(%d, %d, %d, %f) = %v, want %v",
					tt.totalFrames, tt.fpsNum, tt.fpsDen, tt.chunkDurationSecs, result, tt.expected)
			}
		})
	}
}

func TestDedupe(t *testing.T) {
	tests := []struct {
		name     string
		input    []int
		expected []int
	}{
		{
			name:     "no duplicates",
			input:    []int{1, 2, 3},
			expected: []int{1, 2, 3},
		},
		{
			name:     "with duplicates",
			input:    []int{1, 1, 2, 3, 3, 3},
			expected: []int{1, 2, 3},
		},
		{
			name:     "all same",
			input:    []int{5, 5, 5},
			expected: []int{5},
		},
		{
			name:     "empty",
			input:    []int{},
			expected: []int{},
		},
		{
			name:     "single element",
			input:    []int{42},
			expected: []int{42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dedupe(tt.input)
			if !intSliceEqual(result, tt.expected) {
				t.Errorf("dedupe(%v) = %v, want %v",
					tt.input, result, tt.expected)
			}
		})
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
