package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/media"
)

func TestEncodeOneShortPCMAndOutputFailures(t *testing.T) {
	path := tinyWAV(t)
	stream := media.AudioStreamInfo{StreamIndex: 0, Channels: 1}
	output := filepath.Join(t.TempDir(), "audio.opus")
	if err := encodeOne(context.Background(), path, output, stream, 0.25); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, 4)
	if _, err := f.Read(header); err != nil || string(header) != "OggS" {
		t.Fatalf("Opus output header = %q: %v", header, err)
	}
	if err := encodeOne(context.Background(), path, filepath.Join(t.TempDir(), "absent", "audio.opus"), stream, 0.25); err == nil {
		t.Fatal("encoding into missing directory succeeded")
	}
	stream.StreamIndex = 5
	if err := encodeOne(context.Background(), path, filepath.Join(t.TempDir(), "audio.opus"), stream, 0.25); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing stream: %v", err)
	}
}
