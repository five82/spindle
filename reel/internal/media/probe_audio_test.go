package media

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

// A tiny PCM WAV exercises libav probing without a video encode or external tool.
func writeProbeWAV(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.wav")
	data := make([]byte, 48)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], 40)
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:], 1) // mono
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 4)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeAudioOnlyInput(t *testing.T) {
	path := writeProbeWAV(t)
	streams, err := GetAudioStreamInfo(path)
	if err != nil || len(streams) != 1 {
		t.Fatalf("GetAudioStreamInfo = %+v, %v", streams, err)
	}
	if s := streams[0]; s.Channels != 1 || s.CodecName != "pcm_s16le" || s.StreamIndex != 0 || s.Index != 0 {
		t.Fatalf("stream = %+v", s)
	}
	for _, probe := range []struct {
		name string
		run  func() error
	}{
		{"properties", func() error { _, err := GetVideoProperties(path); return err }},
		{"codec", func() error { _, err := GetVideoCodecName(path); return err }},
		{"size", func() error { _, err := GetVideoStreamBytes(path); return err }},
		{"HDR", func() error { _, err := GetStreamHDRInfo(path); return err }},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if err := probe.run(); err == nil || !strings.Contains(err.Error(), "no video stream") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestProbeMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	if _, err := GetAudioStreamInfo(path); err == nil || !strings.Contains(err.Error(), "open failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestRefineHDRFrameOverridesContainer(t *testing.T) {
	depth := uint8(8)
	container := HDRInfo{ColourPrimaries: "bt709", TransferCharacteristics: "bt709", MatrixCoefficients: "bt709", BitDepth: &depth}
	if got := RefineHDR(container, nil); got.IsHDR || *got.BitDepth != 8 {
		t.Fatalf("container-only = %+v", got)
	}
	p, tr, m := int32(9), int32(16), int32(9)
	got := RefineHDR(container, &video.Info{Is10Bit: true, ColorPrimaries: &p, TransferCharacteristics: &tr, MatrixCoefficients: &m})
	if !got.IsHDR || *got.BitDepth != 10 || got.ColourPrimaries != "bt2020" || got.TransferCharacteristics != "smpte2084" || got.MatrixCoefficients != "bt2020nc" {
		t.Fatalf("refined = %+v", got)
	}
	if *container.BitDepth != 8 {
		t.Fatal("mutated container depth")
	}
	unknown := int32(999)
	got = RefineHDR(container, &video.Info{ColorPrimaries: &unknown, TransferCharacteristics: &unknown, MatrixCoefficients: &unknown})
	if got.ColourPrimaries != "bt709" || got.TransferCharacteristics != "bt709" || got.MatrixCoefficients != "bt709" {
		t.Fatalf("unknown frame tags lost container metadata: %+v", got)
	}
}
