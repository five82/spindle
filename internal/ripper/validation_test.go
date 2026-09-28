package ripper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRippedArtifact_EmptyPath(t *testing.T) {
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), "", 0); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateRippedArtifact_NonExistent(t *testing.T) {
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), "/nonexistent/file.mkv", 0); err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestValidateRippedArtifact_Directory(t *testing.T) {
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), t.TempDir(), 0); err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestValidateRippedArtifact_SimpsonsShortVideoLongAudio(t *testing.T) {
	bin := t.TempDir()
	// ffprobe's format duration follows the audio (593s), but the video
	// packet timestamps stop at 112s; MakeMKV claimed a 1377s title.
	probe := `#!/bin/sh
case " $* " in
 *-show_entries\ packet=pts_time*) printf '0.000000\n112.145000\n';;
 *) printf '%s\n' '{"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"format":{"duration":"593.216"}}';;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	file := filepath.Join(t.TempDir(), "D1_t06.mkv")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(86718143); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), file, 1377); err == nil || !strings.Contains(err.Error(), "video spans 112.1s") {
		t.Fatalf("expected video coverage failure, got %v", err)
	}
	if err := h.validateRippedArtifact(context.Background(), file, 100); err != nil {
		t.Fatalf("valid short title rejected: %v", err)
	}
}

func TestValidateRippedArtifact_TooSmall(t *testing.T) {
	h := &Handler{}
	f := filepath.Join(t.TempDir(), "small.mkv")
	if err := os.WriteFile(f, []byte("too small"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.validateRippedArtifact(context.Background(), f, 0); err == nil {
		t.Fatal("expected error for file under 10 MB")
	}
}
