package ffprobe

import (
	"math"
	"testing"
)

func TestResultHelpers(t *testing.T) {
	result := Result{
		Streams: []Stream{
			{CodecType: "video"},
			{CodecType: "audio"},
			{CodecType: "audio"},
		},
		Format: Format{
			Duration: "123.45",
			Size:     "1000",
			BitRate:  "32000",
		},
	}
	if result.VideoStreamCount() != 1 {
		t.Fatalf("expected 1 video stream, got %d", result.VideoStreamCount())
	}
	if result.AudioStreamCount() != 2 {
		t.Fatalf("expected 2 audio streams, got %d", result.AudioStreamCount())
	}
	if result.DurationSeconds() != 123.45 {
		t.Fatalf("unexpected duration: %v", result.DurationSeconds())
	}
	if result.SizeBytes() != 1000 {
		t.Fatalf("unexpected size: %d", result.SizeBytes())
	}
	if result.BitRate() != 32000 {
		t.Fatalf("unexpected bitrate: %d", result.BitRate())
	}
}

func TestResultHelpersHandleInvalidNumbers(t *testing.T) {
	result := Result{
		Format: Format{
			Duration: "bad",
			Size:     "-1",
			BitRate:  "nope",
		},
	}
	if !math.IsNaN(result.DurationSeconds()) {
		t.Fatalf("expected duration NaN, got %v", result.DurationSeconds())
	}
	if result.SizeBytes() != 0 {
		t.Fatalf("expected size 0, got %d", result.SizeBytes())
	}
	if result.BitRate() != 0 {
		t.Fatalf("expected bitrate 0, got %d", result.BitRate())
	}
}
