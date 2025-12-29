package subtitles

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/config"
)

func TestServiceGenerateUsesOpenSubtitlesWhenAvailable(t *testing.T) {
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
00:01:54,000 --> 00:02:00,000
Aligned text
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
	if stub.calledWhisper {
		t.Fatalf("expected whisper transcription to be skipped")
	}
	if !stub.calledFFSubsync {
		t.Fatalf("expected ffsubsync to run before WhisperX alignment")
	}
	if !stub.calledAlignment {
		t.Fatalf("expected alignment pass to run")
	}
	if stub.calledStableTS {
		t.Fatalf("did not expect stable-ts for downloaded subtitles")
	}

	contents, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read subtitles: %v", err)
	}
	output := string(contents)
	if strings.Contains(strings.ToLower(output), "opensubtitles") {
		t.Fatalf("expected advertisement to be removed, got %q", output)
	}
	if !strings.Contains(output, "Aligned text") {
		t.Fatalf("expected aligned text to remain, got %q", output)
	}
	if result.SegmentCount != 1 {
		t.Fatalf("expected segment count 1, got %d", result.SegmentCount)
	}
}

func TestServiceGenerateForceAISkipsOpenSubtitles(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, bytes.Repeat([]byte{0x05, 0x06, 0x07, 0x08}, 1024), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 110, false)
	osStub := &openSubtitlesStub{
		data: []byte(`1
00:00:01,000 --> 00:00:03,000
Example text
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
		ForceAI:    true,
		Context: SubtitleContext{
			Title:     "Example Movie",
			MediaType: "movie",
			TMDBID:    456,
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
	if stub.calledAlignment {
		t.Fatalf("did not expect alignment when skipping OpenSubtitles")
	}
	if osStub.searchCount != 0 {
		t.Fatalf("expected OpenSubtitles search to be skipped, got %d", osStub.searchCount)
	}
	if osStub.downloadCount != 0 {
		t.Fatalf("expected OpenSubtitles download to be skipped, got %d", osStub.downloadCount)
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected AI subtitles to contain segments")
	}
}
