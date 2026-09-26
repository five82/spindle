package video

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecoderRejectsMissingFramesAndNonVideoStreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.y4m")
	var data bytes.Buffer
	data.WriteString("YUV4MPEG2 W32 H32 F25:1 Ip A1:1 C420\n")
	data.WriteString("FRAME\n")
	data.Write(make([]byte, 32*32*3/2))
	data.WriteString("FRAME\n")
	data.Write([]byte{1, 2})
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	inf := &Info{Width: 32, Height: 32, FPSNum: 25, FPSDen: 1}
	buf := make([]byte, FrameSize(inf, nil))
	if err := src.ReadFrame(0, buf, inf, nil); err != nil {
		t.Fatal(err)
	}
	if err := src.ReadFrame(2, buf, inf, nil); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("truncated source read: %v", err)
	}
	// A valid audio-only WAV probes as media but must not become a video source.
	wav := filepath.Join(t.TempDir(), "audio.wav")
	header := make([]byte, 44+32)
	copy(header, "RIFFD\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x20\x00\x00\x00")
	if err := os.WriteFile(wav, header, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(wav, 1); err == nil || !strings.Contains(err.Error(), "no video") {
		t.Fatalf("audio-only source: %v", err)
	}
}
