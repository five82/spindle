package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tinyWAV(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "samples.wav")
	data := make([]byte, 44+16000)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDecoderShortPCMAndFailurePaths(t *testing.T) {
	path := tinyWAV(t)
	if _, err := openDecoder(path, 99); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing stream: %v", err)
	}
	dec, err := openDecoder(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.close()
	if dec.channels != 1 {
		t.Fatalf("channels = %d", dec.channels)
	}
	calls := 0
	if err := dec.decodeTo(context.Background(), 0, func([]float32) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("zero limit: %v calls=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := dec.decodeTo(ctx, 100, func([]float32) error { t.Fatal("callback after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	// Open a new decoder for each run so the input starts at the beginning.
	for _, tt := range []struct {
		limit int64
		fail  bool
	}{{1, false}, {100, false}, {100, true}} {
		d, err := openDecoder(path, 0)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		callbackErr := errors.New("callback failure")
		err = d.decodeTo(context.Background(), tt.limit, func(samples []float32) error {
			count += len(samples)
			if tt.fail {
				return callbackErr
			}
			return nil
		})
		d.close()
		if count == 0 || (tt.fail && !errors.Is(err, callbackErr)) || (!tt.fail && err != nil) {
			t.Errorf("limit %d fail %t: samples=%d err=%v", tt.limit, tt.fail, count, err)
		}
	}
	dec.close()
	(*decoder)(nil).close()
}
