package encode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

func TestGrainSampleMeasuresIVFPayloadOnlyWithoutEncoding(t *testing.T) {
	source := writeTestY4M(t, 32, 32, 3)
	inf, err := video.Probe(source)
	if err != nil {
		t.Fatal(err)
	}
	in := GrainGateInput{InputPath: source, Info: inf}
	ch := chunk.Chunk{Idx: 3, Start: 0, End: 3}
	output := filepath.Join(t.TempDir(), "sample.ivf")
	writeIVF := func(_ video.FrameReader) error {
		var data bytes.Buffer
		data.Write(make([]byte, 32))
		for _, size := range []uint32{12, 20} {
			var frame [12]byte
			binary.LittleEndian.PutUint32(frame[:], size)
			data.Write(frame[:])
			data.Write(make([]byte, size))
		}
		return os.WriteFile(output, data.Bytes(), 0600)
	}
	bpp, err := measureChunkBPPWithEncode(in, ch, output, 32, 32, func(reader video.FrameReader) error {
		if err := reader.ReadFrame(2, make([]byte, video.FrameSize(inf, nil))); err != nil {
			return err
		}
		return writeIVF(reader)
	})
	want := float64(32*8) / float64(32*32*ch.Frames())
	if err != nil || math.Abs(bpp-want) > 1e-9 {
		t.Fatalf("payload BPP = %.7f (want %.7f): %v", bpp, want, err)
	}
	sentinel := errors.New("encoder failed")
	if _, err := measureChunkBPPWithEncode(in, ch, output, 32, 32, func(video.FrameReader) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("encode error: %v", err)
	}
	in.InputPath = "missing.y4m"
	if _, err := measureChunkBPPWithEncode(in, ch, output, 32, 32, writeIVF); err == nil || !strings.Contains(err.Error(), "open source") {
		t.Fatalf("source error: %v", err)
	}
	in.InputPath = source
	if _, err := measureChunkBPPWithEncode(in, ch, filepath.Join(t.TempDir(), "missing.ivf"), 32, 32, func(video.FrameReader) error { return nil }); err == nil || !strings.Contains(err.Error(), "open grain gate sample") {
		t.Fatalf("missing IVF: %v", err)
	}
	if err := os.WriteFile(output, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := measureChunkBPPWithEncode(in, ch, output, 32, 32, func(video.FrameReader) error { return nil }); err == nil || !strings.Contains(err.Error(), "measure grain gate") {
		t.Fatalf("invalid IVF: %v", err)
	}
	if _, err := measureChunkBPPWithEncode(in, chunk.Chunk{Idx: 3}, output, 32, 32, writeIVF); err == nil || !strings.Contains(err.Error(), "no pixels") {
		t.Fatalf("empty chunk: %v", err)
	}
}
