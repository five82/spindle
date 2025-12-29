package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/config"
)

func TestServiceGenerateUsesTranscriptCache(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "episode.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 30, false)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Paths.WhisperXCacheDir = filepath.Join(tmp, "cache")
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	key := "queue-1/s01e01"
	if err := service.ensureTranscriptCache(); err != nil {
		t.Fatalf("ensure cache: %v", err)
	}
	if service.transcriptCache == nil {
		t.Fatalf("expected transcript cache to be initialized")
	}
	cached := []byte("1\n00:00:01,000 --> 00:00:02,000\nHowdy!\n")
	if _, err := service.transcriptCache.Store(key, "en", 1, cached); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath:               source,
		WorkDir:                  filepath.Join(tmp, "work"),
		OutputDir:                filepath.Join(tmp, "out"),
		BaseName:                 "episode",
		ForceAI:                  true,
		TranscriptKey:            key,
		AllowTranscriptCacheRead: true,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg to run for cache alignment")
	}
	if stub.calledWhisper {
		t.Fatalf("did not expect whisperx to run when cache hit")
	}
	if result.SegmentCount != 1 {
		t.Fatalf("expected cached segment count 1, got %d", result.SegmentCount)
	}
	contents, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read cached output: %v", err)
	}
	if string(contents) != string(cached) {
		t.Fatalf("cached output mismatch: %q", contents)
	}
}

func TestServiceGenerateStoresTranscriptInCache(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "episode.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 50, false)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Paths.WhisperXCacheDir = filepath.Join(tmp, "cache")
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	key := "queue-2/s01e02"
	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath:                source,
		WorkDir:                   filepath.Join(tmp, "work"),
		OutputDir:                 filepath.Join(tmp, "out"),
		BaseName:                  "episode",
		ForceAI:                   true,
		TranscriptKey:             key,
		AllowTranscriptCacheWrite: true,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx to run when cache miss")
	}
	if err := service.ensureTranscriptCache(); err != nil {
		t.Fatalf("ensure cache: %v", err)
	}
	if service.transcriptCache == nil {
		t.Fatalf("expected transcript cache instance")
	}
	data, meta, ok, err := service.transcriptCache.Load(key)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache entry to exist")
	}
	if meta.Segments != result.SegmentCount {
		t.Fatalf("expected cached segments %d, got %d", result.SegmentCount, meta.Segments)
	}
	output, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(output) != string(data) {
		t.Fatalf("cache data mismatch")
	}
}
