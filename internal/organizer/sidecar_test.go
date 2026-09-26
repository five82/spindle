package organizer

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestCopySidecarSubtitleCopiesOnlyMatchingSRTs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mkv")
	dest := filepath.Join(dir, "library", "movie.mkv")
	if err := os.Mkdir(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"source.en.srt": "English", "source.fr.srt": "French",
		"source.en.ass": "not SRT", "other.en.srt": "other video",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copySidecarSubtitle(logger, src, dest)
	for suffix, want := range map[string]string{".en.srt": "English", ".fr.srt": "French"} {
		data, err := os.ReadFile(filepath.Join(dir, "library", "movie"+suffix))
		if err != nil || string(data) != want {
			t.Errorf("copied %s = %q, %v", suffix, data, err)
		}
	}
	for _, suffix := range []string{".en.ass", ".en.srt.bak"} {
		if _, err := os.Stat(filepath.Join(dir, "library", "movie"+suffix)); !os.IsNotExist(err) {
			t.Errorf("unexpected sidecar %s: %v", suffix, err)
		}
	}
	copySidecarSubtitle(logger, filepath.Join(dir, "missing.mkv"), dest)
}

func TestCopySidecarSubtitleLogsCopyFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(filepath.Join(dir, "source.en.srt"), []byte("subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The library directory does not exist. Failure is logged, not fatal.
	copySidecarSubtitle(logger, src, filepath.Join(dir, "missing", "movie.mkv"))
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("unexpected library directory: %v", err)
	}
}
