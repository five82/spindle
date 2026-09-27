package audio

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecoderRejectsInvalidStreamParameters(t *testing.T) {
	path := tinyWAV(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		edit       func([]byte)
	}{
		{"unsupported format", "unsupported codec", func(wav []byte) { binary.LittleEndian.PutUint16(wav[20:], 0xffff) }},
		{"zero sample rate", "open failed", func(wav []byte) { binary.LittleEndian.PutUint32(wav[24:], 0) }},
		{"zero channels", "codec open failed", func(wav []byte) { binary.LittleEndian.PutUint16(wav[22:], 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wav := append([]byte(nil), original...)
			tc.edit(wav)
			bad := filepath.Join(t.TempDir(), "bad.wav")
			if err := os.WriteFile(bad, wav, 0600); err != nil {
				t.Fatal(err)
			}
			d, err := openDecoder(bad, 0)
			if d != nil {
				d.close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decoder error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecoderStereoPCMResamplesWithoutLosingChannels(t *testing.T) {
	// 8 kHz, 16-bit stereo, one second per channel. Resampling to 48 kHz
	// should preserve channel pairing.
	wav := make([]byte, 44+8000*4)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], 8000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(wav)-44))
	right := int16(-1000)
	for i := range 8000 {
		binary.LittleEndian.PutUint16(wav[44+i*4:], 1000)
		binary.LittleEndian.PutUint16(wav[46+i*4:], uint16(right))
	}
	path := filepath.Join(t.TempDir(), "stereo.wav")
	if err := os.WriteFile(path, wav, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := openDecoder(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if d.channels != 2 {
		t.Fatalf("decoded channels = %d", d.channels)
	}
	var frames int
	if err := d.decodeTo(context.Background(), 100000, func(pcm []float32) error {
		if len(pcm)%2 != 0 {
			t.Fatalf("odd PCM sample count: %d", len(pcm))
		}
		for i := 0; i < len(pcm); i += 2 {
			if pcm[i] <= 0 || pcm[i+1] >= 0 {
				t.Fatalf("left/right channels swapped or corrupted: %v", pcm[i:i+2])
			}
		}
		frames += len(pcm) / 2
		return nil
	}); err != nil || frames != 48000 {
		t.Fatalf("decoded frames=%d err=%v", frames, err)
	}
}
