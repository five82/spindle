package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlignmentOnDeterministicRawFrames(t *testing.T) {
	// Y4M needs no encoder. The 210-frame clip reaches the first spread probe
	// (frame 200) while remaining small enough for the concurrent seek test.
	path := filepath.Join(t.TempDir(), "source.y4m")
	var clip bytes.Buffer
	clip.WriteString("YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 210; i++ {
		clip.WriteString("FRAME\n")
		clip.Write(bytes.Repeat([]byte{byte(i)}, 16))
		clip.Write(bytes.Repeat([]byte{128}, 8))
	}
	if err := os.WriteFile(path, clip.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	oldArgs, oldCrop, oldOut := os.Args, cropRect, os.Stdout
	defer func() { os.Args, cropRect, os.Stdout = oldArgs, oldCrop, oldOut }()
	cropRect = nil
	os.Args = []string{"aligncheck", path, "1"}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	main()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sequential: 0/1 mismatched", "concurrent: 0/160 reads wrong", "RESULT: ReadFrame is aligned and deterministic"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("missing %q in %s", want, fmt.Sprintf("%.500s", output))
		}
	}
}
