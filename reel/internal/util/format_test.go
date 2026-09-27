package util

import (
	"math"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes uint64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.00 KiB"},
		{1536, "1.50 KiB"},
		{1024 * 1024, "1.00 MiB"},
		{1024 * 1024 * 1024, "1.00 GiB"},
		{1024 * 1024 * 1024 * 2, "2.00 GiB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{0, "00:00:00"},
		{59, "00:00:59"},
		{60, "00:01:00"},
		{3599, "00:59:59"},
		{3600, "01:00:00"},
		{3661, "01:01:01"},
		{86399, "23:59:59"},
		{86400, "24:00:00"},
		{-1, "??:??:??"},
		{math.NaN(), "??:??:??"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatDuration(tt.seconds)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestParseFFmpegTime(t *testing.T) {
	tests := []struct {
		input  string
		want   float64
		wantOk bool
	}{
		{"00:00:00", 0, true},
		{"00:00:01", 1, true},
		{"00:01:00", 60, true},
		{"01:00:00", 3600, true},
		{"01:02:03", 3723, true},
		{"00:00:00.5", 0.5, true},
		{"01:30:45.75", 5445.75, true},
		{"", 0, false},
		{"00:00", 0, false},
		{"invalid", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := ParseFFmpegTime(tt.input)
			if ok != tt.wantOk {
				t.Errorf("ParseFFmpegTime(%q) ok = %v, want %v", tt.input, ok, tt.wantOk)
				return
			}
			if ok && got != tt.want {
				t.Errorf("ParseFFmpegTime(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCalculateSizeReduction(t *testing.T) {
	tests := []struct {
		input  uint64
		output uint64
		want   float64
	}{
		{100, 50, 50},
		{1000, 250, 75},
		{0, 100, 0},
		{100, 100, 0},
		{100, 150, -50}, // Output larger = negative reduction
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := CalculateSizeReduction(tt.input, tt.output)
			if got != tt.want {
				t.Errorf("CalculateSizeReduction(%d, %d) = %v, want %v", tt.input, tt.output, got, tt.want)
			}
		})
	}
}
