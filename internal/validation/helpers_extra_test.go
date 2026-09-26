package validation

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/media"
)

func TestValidateDimensionsAndDuration(t *testing.T) {
	for _, tt := range []struct {
		w, h, ew, eh uint32
		ok           bool
		text         string
	}{{1920, 1080, 1920, 1080, true, "Dimensions match"}, {1920, 1080, 1280, 720, false, "Dimension mismatch"}} {
		ok, msg := validateDimensions(tt.w, tt.h, tt.ew, tt.eh)
		if ok != tt.ok || !strings.Contains(msg, tt.text) {
			t.Errorf("dimensions: %t %s", ok, msg)
		}
	}
	for _, tt := range []struct {
		actual, expected float64
		ok               bool
	}{{101, 100, true}, {99, 100, true}, {101.01, 100, false}} {
		ok, _ := validateDuration(tt.actual, tt.expected)
		if ok != tt.ok {
			t.Errorf("duration %v vs %v = %t", tt.actual, tt.expected, ok)
		}
	}
}

func TestValidateAudioCodecAndCount(t *testing.T) {
	one := 1
	two := 2
	for _, tt := range []struct {
		streams     []media.AudioStreamInfo
		expected    *int
		opus, count bool
		message     string
	}{
		{nil, &one, true, false, "No audio tracks"},
		{[]media.AudioStreamInfo{{CodecName: "OpUs"}}, &one, true, true, "Audio track is Opus"},
		{[]media.AudioStreamInfo{{CodecName: "aac"}}, nil, false, true, "expected Opus"},
		{[]media.AudioStreamInfo{{CodecName: "opus"}, {CodecName: "opus"}}, &two, true, true, "all Opus"},
		{[]media.AudioStreamInfo{{CodecName: "opus"}, {CodecName: "aac"}}, &one, false, false, "opus, aac"},
	} {
		opus, count, codecs, msg := validateAudio(tt.streams, tt.expected)
		if opus != tt.opus || count != tt.count || !strings.Contains(msg, tt.message) || len(codecs) != len(tt.streams) {
			t.Errorf("audio %v: %t %t %v %q", tt.streams, opus, count, codecs, msg)
		}
	}
}

func TestValidateOutputVideoMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.mkv")
	if result, err := ValidateOutputVideo(missing, missing, Options{}); err == nil || result != nil || !strings.Contains(err.Error(), "failed to get output video properties") {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if ok, name := validateVideoCodec(missing); ok || name != "" {
		t.Fatalf("codec: %t %q", ok, name)
	}
	if ok, depth, fmt := validateBitDepth(missing); ok || depth != nil || fmt != "" {
		t.Fatalf("depth: %t %v %q", ok, depth, fmt)
	}
}
