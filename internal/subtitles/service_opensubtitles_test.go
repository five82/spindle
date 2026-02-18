package subtitles

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/config"
)

func TestServiceGenerateAlwaysUsesWhisperX(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 1024), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 120, false)
	osStub := &openSubtitlesStub{
		data: []byte(`1
00:00:01,000 --> 00:00:03,000
www.opensubtitles.org

2
00:00:10,000 --> 00:00:15,000
First line of dialogue.
`),
	}

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.OpenSubtitlesEnabled = true
	cfg.Subtitles.OpenSubtitlesAPIKey = "k"
	cfg.Subtitles.OpenSubtitlesUserAgent = "Spindle/test"
	cfg.Subtitles.OpenSubtitlesLanguages = []string{"en"}

	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithOpenSubtitlesClient(osStub),
		WithoutDependencyCheck(),
	)

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
		Context: SubtitleContext{
			Title:     "Example Movie",
			MediaType: "movie",
			TMDBID:    123,
			Year:      "2024",
		},
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisper transcription to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	// OpenSubtitles search/download should NOT be called for regular subtitles
	if osStub.searchCount != 0 {
		t.Fatalf("expected OpenSubtitles search to be skipped, got %d", osStub.searchCount)
	}
	if osStub.downloadCount != 0 {
		t.Fatalf("expected OpenSubtitles download to be skipped, got %d", osStub.downloadCount)
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected WhisperX subtitles to contain segments")
	}
	if result.Source != "whisperx" {
		t.Fatalf("expected source to be whisperx, got %q", result.Source)
	}
}
