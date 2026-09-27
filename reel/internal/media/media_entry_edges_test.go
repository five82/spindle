package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMediaProbeOnRawVideoAndMissingInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.y4m")
	clip := append([]byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\nFRAME\n"), bytes.Repeat([]byte{128}, 16*16*3/2)...)
	if err := os.WriteFile(path, clip, 0600); err != nil {
		t.Fatal(err)
	}
	props, err := GetVideoProperties(path)
	if err != nil || props.Width != 16 || props.Height != 16 {
		t.Fatalf("video properties: %+v, %v", props, err)
	}
	size, err := GetVideoStreamBytes(path)
	if err != nil || size == 0 {
		t.Fatalf("video stream bytes: %d %v", size, err)
	}
	if _, err := GetHDRInfo(path); err != nil {
		t.Fatalf("HDR info: %v", err)
	}
	if _, err := GetVideoProperties(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing video")
	}
	if _, err := GetVideoStreamBytes(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing stream")
	}
	if _, err := GetHDRInfo(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing HDR source")
	}
}
