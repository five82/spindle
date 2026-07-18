package apply

import (
	"math"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestParseDurationSeconds(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want float64
		ok   bool
	}{
		{name: "seconds", raw: "291.530000", want: 291.53, ok: true},
		{name: "matroska duration tag", raw: "02:10:12.980000000", want: 7812.98, ok: true},
		{name: "empty", raw: "", ok: false},
		{name: "not available", raw: "N/A", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseDurationSeconds(tt.raw)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && math.Abs(got-tt.want) > 0.001 {
				t.Fatalf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateAudioDurationsDetectsTruncatedMatroskaAudio(t *testing.T) {
	result := &ffprobe.Result{
		Format: ffprobe.Format{Duration: "7813.000000"},
		Streams: []ffprobe.Stream{
			{CodecType: "video"},
			{CodecType: "audio", Tags: map[string]string{"DURATION": "00:04:51.530000000"}},
			{CodecType: "audio", Tags: map[string]string{"DURATION": "02:10:13.000000000"}},
		},
	}

	err := validateAudioDurations("movie.mkv", result)
	if err == nil {
		t.Fatal("expected truncated audio duration error")
	}
	if !strings.Contains(err.Error(), "audio stream 0 duration 291.530s differs from video 7813.000s") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAudioDurationsAcceptsMatchingAudio(t *testing.T) {
	result := &ffprobe.Result{
		Format: ffprobe.Format{Duration: "7813.000000"},
		Streams: []ffprobe.Stream{
			{CodecType: "video"},
			{CodecType: "audio", Tags: map[string]string{"DURATION": "02:10:12.980000000"}},
			{CodecType: "audio", Duration: "7813.000000"},
		},
	}

	if err := validateAudioDurations("movie.mkv", result); err != nil {
		t.Fatalf("validateAudioDurations returned error: %v", err)
	}
}

func TestValidateAudioDurationsToleratesShortCommentary(t *testing.T) {
	// The Secret of My Success (Kino): source commentary ends 34s before the
	// video does. Commentary-flagged streams may run short of the video.
	result := &ffprobe.Result{
		Format: ffprobe.Format{Duration: "6614.674000"},
		Streams: []ffprobe.Stream{
			{CodecType: "video"},
			{CodecType: "audio", Duration: "6614.674000"},
			{CodecType: "audio", Duration: "6580.296000", Disposition: map[string]int{"comment": 1}},
		},
	}

	if err := validateAudioDurations("movie.mkv", result); err != nil {
		t.Fatalf("validateAudioDurations returned error: %v", err)
	}
}

func TestValidateAudioDurationsFlagsTruncatedCommentary(t *testing.T) {
	result := &ffprobe.Result{
		Format: ffprobe.Format{Duration: "6614.674000"},
		Streams: []ffprobe.Stream{
			{CodecType: "video"},
			{CodecType: "audio", Duration: "6614.674000"},
			{CodecType: "audio", Duration: "291.530000", Disposition: map[string]int{"comment": 1}},
		},
	}

	if err := validateAudioDurations("movie.mkv", result); err == nil {
		t.Fatal("expected truncated commentary duration error")
	}
}

func TestValidateAudioDurationsFlagsLongCommentary(t *testing.T) {
	result := &ffprobe.Result{
		Format: ffprobe.Format{Duration: "6614.674000"},
		Streams: []ffprobe.Stream{
			{CodecType: "video"},
			{CodecType: "audio", Duration: "6700.000000", Disposition: map[string]int{"comment": 1}},
		},
	}

	if err := validateAudioDurations("movie.mkv", result); err == nil {
		t.Fatal("expected over-long commentary duration error")
	}
}

func TestValidateAudioDurationsFallsBackToVideoStreamDuration(t *testing.T) {
	result := &ffprobe.Result{
		Streams: []ffprobe.Stream{
			{CodecType: "video", Tags: map[string]string{"DURATION": "02:10:13.000000000"}},
			{CodecType: "audio", Tags: map[string]string{"DURATION": "00:04:51.530000000"}},
		},
	}

	if err := validateAudioDurations("movie.mkv", result); err == nil {
		t.Fatal("expected duration mismatch error")
	}
}
