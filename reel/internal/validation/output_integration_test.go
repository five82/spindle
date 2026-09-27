package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tiny raw Y4M exercises the native media probes and validation assembly
// without running an encoder or depending on an external ffmpeg executable.
func TestValidateOutputVideoFromRawClip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.y4m")
	frame := append([]byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\nFRAME\n"), make([]byte, 16*16*3/2)...)
	if err := os.WriteFile(path, frame, 0600); err != nil {
		t.Fatal(err)
	}
	dims := [2]uint32{16, 16}
	aspect := [2]uint32{1, 1}
	duration := 1.0 / 25
	tracks := 0
	for _, tc := range []struct {
		name                          string
		opts                          Options
		crop, aspect, duration, audio bool
	}{
		{"no expectations", Options{}, true, true, true, true},
		{"matches", Options{ExpectedDimensions: &dims, ExpectedDisplayAspect: &aspect, ExpectedDuration: &duration, ExpectedAudioTracks: &tracks}, true, true, true, true},
		{"mismatch", Options{ExpectedDimensions: &[2]uint32{32, 32}, ExpectedDisplayAspect: &[2]uint32{16, 9}, ExpectedDuration: ptr(20.0), ExpectedAudioTracks: ptr(1)}, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ValidateOutputVideo(path, path, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.IsCropCorrect != tc.crop || result.IsAspectCorrect != tc.aspect || result.IsDurationCorrect != tc.duration || result.IsAudioTrackCountCorrect != tc.audio {
				t.Fatalf("validation flags: %+v", result)
			}
			if result.IsAV1 || result.Is10Bit {
				t.Fatalf("raw 8-bit Y4M reported as AV1/10-bit: %+v", result)
			}
			if !result.IsSyncPreserved {
				t.Fatalf("audio-less clip has sync failure: %+v", result)
			}
		})
	}
	if _, err := ValidateOutputVideo(path, filepath.Join(t.TempDir(), "missing"), Options{}); err == nil || !strings.Contains(err.Error(), "output video properties") {
		t.Fatalf("missing output: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
