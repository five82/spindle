package video

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanarNon420SourceNormalizesForEncodingAndScoring(t *testing.T) {
	for _, tc := range []struct {
		name, chroma string
		planes       int
	}{
		{"422", "C422", 2}, {"444", "C444", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const w, h = 32, 24
			var data bytes.Buffer
			data.WriteString("YUV4MPEG2 W32 H24 F25:1 Ip A1:1 " + tc.chroma + "\n")
			for i := range 3 {
				data.WriteString("FRAME\n")
				data.Write(bytes.Repeat([]byte{byte(i*20 + 64)}, w*h))
				data.Write(bytes.Repeat([]byte{128}, w*h*(tc.planes-1)))
			}
			path := filepath.Join(t.TempDir(), "planar.y4m")
			if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := Probe(path)
			if err != nil {
				t.Fatal(err)
			}
			src, err := Open(path, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			buf := make([]byte, FrameSize(info, nil))
			for _, idx := range []int{0, 1, 2, 0} {
				if err := src.ReadFrame(idx, buf, info, nil); err != nil {
					t.Fatalf("frame %d: %v", idx, err)
				}
				if binary.LittleEndian.Uint16(buf) != uint16((idx*20+64)*4) {
					t.Fatalf("frame %d luma %v", idx, buf[:2])
				}
			}
			luma, err := src.ReadLumaFrame(1, info)
			if err != nil || luma == nil || luma.Width != w || luma.Height != h {
				t.Fatalf("luma %+v: %v", luma, err)
			}
		})
	}
}
