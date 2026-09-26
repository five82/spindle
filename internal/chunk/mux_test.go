package chunk

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nativeaudio "github.com/five82/reel/internal/audio"
	"github.com/five82/reel/internal/media"
)

func TestMuxFinalMissingInputs(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mkv")
	if err := MuxFinal("missing", dir, out, nil, "", 1); err == nil || !strings.Contains(err.Error(), "video file not found") {
		t.Fatalf("missing video: %v", err)
	}
	if err := os.WriteFile(GetVideoPath(dir), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := MuxFinal("missing", dir, out, []nativeaudio.EncodedStream{{Path: filepath.Join(dir, "absent.opus")}}, "", 1); err == nil || !strings.Contains(err.Error(), "audio file not found") {
		t.Fatalf("missing audio: %v", err)
	}
	if err := MuxFinal("missing", dir, out, nil, "", 1); err == nil {
		t.Fatal("invalid source must fail")
	}
}

func TestMuxFinalRejectsInvalidVideoPacket(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.y4m")
	// A raw Y4M source and malformed IVF exercise container setup and packet
	// failure without running an encoder.
	y4m := append([]byte("YUV4MPEG2 W2 H2 F25:1 Ip A1:1 C420\nFRAME\n"), []byte{16, 16, 16, 16, 128, 128}...)
	if err := os.WriteFile(source, y4m, 0600); err != nil {
		t.Fatal(err)
	}
	ivf := make([]byte, 32+12+4)
	copy(ivf, "DKIF")
	binary.LittleEndian.PutUint16(ivf[6:], 32)
	copy(ivf[8:], "AV01")
	binary.LittleEndian.PutUint16(ivf[12:], 2)
	binary.LittleEndian.PutUint16(ivf[14:], 2)
	binary.LittleEndian.PutUint32(ivf[16:], 25)
	binary.LittleEndian.PutUint32(ivf[20:], 1)
	binary.LittleEndian.PutUint32(ivf[24:], 1)
	binary.LittleEndian.PutUint32(ivf[32:], 4)
	copy(ivf[44:], []byte{0x12, 0, 0, 0})
	if err := os.WriteFile(GetVideoPath(dir), ivf, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output.mkv")
	if err := MuxFinal(source, dir, output, nil, "1:1", 0.04); err == nil || !strings.Contains(err.Error(), "write packet") {
		t.Fatalf("expected invalid packet error, got %v", err)
	}
	// A tiny PCM WAV exercises audio stream copying, language/title metadata,
	// disposition and interleaving against the malformed video packet.
	wav := make([]byte, 44+2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 48000)
	binary.LittleEndian.PutUint32(wav[28:], 96000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 2)
	audioPath := filepath.Join(dir, "audio.wav")
	if err := os.WriteFile(audioPath, wav, 0600); err != nil {
		t.Fatal(err)
	}
	stream := nativeaudio.EncodedStream{Path: audioPath, Info: media.AudioStreamInfo{Language: "eng", Title: "Test", Disposition: media.StreamDisposition{Default: 1}}}
	if err := MuxFinal(source, dir, filepath.Join(dir, "audio-output.mkv"), []nativeaudio.EncodedStream{stream}, "", 0.04); err == nil || !strings.Contains(err.Error(), "write packet") {
		t.Fatalf("expected invalid packet error with audio, got %v", err)
	}
}

func TestNativeMuxerInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	if _, err := newNativeMuxer(filepath.Join(dir, "unknown.extension")); err == nil {
		t.Fatal("unknown output container")
	}
	m, err := newNativeMuxer(filepath.Join(dir, "out.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if err := m.openSource(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing source")
	}
	if err := m.addVideo(filepath.Join(dir, "absent"), ""); err == nil {
		t.Fatal("missing video")
	}
	if err := m.addAudio(nativeaudio.EncodedStream{Path: filepath.Join(dir, "absent")}); err == nil {
		t.Fatal("missing audio")
	}
	if err := m.copySourceMetadata(0); err != nil {
		t.Fatal(err)
	}
	if err := m.muxPackets(); err != nil {
		t.Fatal(err)
	}
}
