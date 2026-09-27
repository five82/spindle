package util

import (
	"strings"
	"testing"
)

func TestReadableSizesAndIntegralDurations(t *testing.T) {
	for _, tc := range []struct {
		bytes uint64
		want  string
	}{
		{0, "0.00 MB (0.00 GB)"}, {MiB, "1.00 MB (0.00 GB)"}, {GiB, "1024.00 MB (1.00 GB)"},
	} {
		if got := FormatBytesReadable(tc.bytes); got != tc.want {
			t.Errorf("%d bytes = %q, want %q", tc.bytes, got, tc.want)
		}
	}
	if got := FormatDurationFromSecs(3661); got != "01:01:01" {
		t.Fatalf("duration = %s", got)
	}
	for _, input := range []string{"broken", "X:00:00", "00:X:00", "00:00:X"} {
		if _, ok := ParseFFmpegTime(input); ok {
			t.Errorf("parsed invalid time %s", input)
		}
	}
	if !strings.Contains(FormatBytesReadable(10*GiB), "10.00 GB") {
		t.Fatal("10 GiB not readable")
	}
}
