package processing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/media"
)

func TestProcessVideosReportsTruncatedClipWithoutOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.y4m")
	data := append([]byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\nFRAME\n"), 0x12, 0x34)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	// The header describes a video, but its only frame is incomplete. The
	// orchestrator must not report success or leave an output file.
	if _, err := media.GetVideoProperties(path); err != nil {
		t.Fatalf("test fixture must be probeable as a video container: %v", err)
	}
	cfg := config.NewConfig("", filepath.Join(dir, "out"), "")
	rep := &clipReporter{}
	results, err := ProcessVideos(context.Background(), cfg, []string{path}, "", rep)
	if err != nil || len(results) != 0 || len(rep.errors) != 1 || !strings.Contains(rep.errors[0].Message, "Failed to encode") {
		t.Fatalf("truncated input: results=%+v err=%v errors=%+v", results, err, rep.errors)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "broken.mkv")); !os.IsNotExist(err) {
		t.Fatalf("unexpected output for truncated clip: %v", err)
	}
}
