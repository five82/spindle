package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecoderReadsToEOFAndCanReopen(t *testing.T) {
	path := tinyWAV(t)
	for range 2 {
		d, err := openDecoder(path, 0)
		if err != nil {
			t.Fatal(err)
		}
		count, calls := 0, 0
		err = d.decodeTo(context.Background(), 100000, func(pcm []float32) error {
			calls++
			count += len(pcm)
			return nil
		})
		d.close()
		if err != nil || count != 48000 || calls == 0 {
			t.Fatalf("EOF: %d samples, %d calls, %v", count, calls, err)
		}
	}
}

func TestDecoderRejectsNonAudioAndBrokenContainers(t *testing.T) {
	for _, tc := range []struct{ name, contents string }{
		{"empty", ""}, {"invalid", "not a media file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "broken.wav")
			if err := os.WriteFile(path, []byte(tc.contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := openDecoder(path, 0); err == nil || !strings.Contains(err.Error(), "open failed") {
				t.Fatalf("bad input: %v", err)
			}
		})
	}
}
