package ripper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

func TestValidateRippedArtifact_EmptyPath(t *testing.T) {
	h := &Handler{}
	_, err := h.validateRippedArtifact(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateRippedArtifact_NonExistent(t *testing.T) {
	h := &Handler{}
	_, err := h.validateRippedArtifact(context.Background(), "/nonexistent/file.mkv")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestValidateRippedArtifact_Directory(t *testing.T) {
	h := &Handler{}
	_, err := h.validateRippedArtifact(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestValidateRippedArtifact_TooSmall(t *testing.T) {
	h := &Handler{}
	f := filepath.Join(t.TempDir(), "small.mkv")
	if err := os.WriteFile(f, []byte("too small"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.validateRippedArtifact(context.Background(), f)
	if err == nil {
		t.Fatal("expected error for file under 10 MB")
	}
}

func TestRecordEncodeTierSignal_UHDProbeCorrectsUpward(t *testing.T) {
	// Scan missed the resolution; the rip probe corrects false -> true.
	env := &ripspec.Envelope{Metadata: ripspec.Metadata{UHD: false}}
	probe := &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "audio"},
		{CodecType: "video", Width: 3840, Height: 2160},
	}}

	recordEncodeTierSignal(testLogger(), env, "/staging/movie.mkv", probe, nil)

	if !env.Metadata.UHD {
		t.Fatal("expected Metadata.UHD = true for 3840x2160 probe")
	}
}

func TestRecordEncodeTierSignal_HDProbeCorrectsDownward(t *testing.T) {
	// Scan lied UHD; the rip probe corrects true -> false.
	env := &ripspec.Envelope{Metadata: ripspec.Metadata{UHD: true}}
	probe := &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "video", Width: 1920, Height: 1080},
	}}

	recordEncodeTierSignal(testLogger(), env, "/staging/movie.mkv", probe, nil)

	if env.Metadata.UHD {
		t.Fatal("expected Metadata.UHD = false for 1920x1080 probe")
	}
}

func TestRecordEncodeTierSignal_ProbeFailureKeepsScanStamp(t *testing.T) {
	for _, scanStamp := range []bool{false, true} {
		env := &ripspec.Envelope{Metadata: ripspec.Metadata{UHD: scanStamp}}

		recordEncodeTierSignal(testLogger(), env, "/staging/movie.mkv", nil, context.DeadlineExceeded)

		if env.Metadata.UHD != scanStamp {
			t.Fatalf("expected Metadata.UHD to stay %v on probe failure, got %v", scanStamp, env.Metadata.UHD)
		}
	}
}
