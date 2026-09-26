package encoder

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestInitialEncodingSnapshotCapturesVideoAndSize(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"codec_type\":\"audio\",\"codec_name\":\"aac\"},{\"codec_type\":\"video\",\"codec_name\":\"hevc\",\"width\":1920,\"height\":1080},{\"codec_type\":\"video\",\"width\":640,\"height\":480}],\"format\":{\"size\":\"1048576\"}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	job := stage.AssetJob{Key: "main", Input: ripspec.Asset{Path: "movie.mkv"}}
	snap := New(&config.Config{}).initialEncodingSnapshot(context.Background(), testEncoderLogger(), job)
	if snap.InputFile != "movie.mkv" || snap.Resolution != "1920x1080" || snap.OriginalSize != 1048576 || snap.Substage != "initializing" {
		t.Fatalf("snapshot: %+v", snap)
	}
}
