package subtitles

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/config"
)

func TestServiceGenerateProducesStableTSSRT_CPUMode(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 120, false)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected non-zero segments")
	}
	if _, err := os.Stat(result.SubtitlePath); err != nil {
		t.Fatalf("expected subtitle file to exist: %v", err)
	}
	contents, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read srt: %v", err)
	}
	for _, raw := range strings.Split(string(contents), "\n") {
		if strings.HasPrefix(raw, " ") && strings.TrimSpace(raw) != "" && !strings.Contains(raw, "-->") {
			t.Fatalf("unexpected leading space: %q", raw)
		}
	}
	t.Logf("\n%s", string(contents))
	if result.Duration != 120*time.Second {
		t.Fatalf("unexpected duration: %s", result.Duration)
	}
	if !strings.Contains(string(contents), "General Kenobi") {
		t.Fatalf("expected subtitle content to include segment text")
	}
	if strings.Contains(string(contents), "<i>") || strings.Contains(string(contents), "\u266A") {
		t.Fatalf("unexpected lyric styling in output")
	}
}

func TestServiceGenerateUsesCUDAArgsWhenEnabled(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 60, true)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.WhisperXCUDAEnabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	if _, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
	}
}

func TestServiceGenerateFallsBackToWhisperSRT(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 75, false)
	stub.stableTSError = fmt.Errorf("stable-ts boom")

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	t.Setenv("SPD_DEBUG_SUBTITLES_KEEP", "1")

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts command to be attempted")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
	}

	whisperSRT := filepath.Join(tmp, "work", "whisperx", "primary_audio.srt")
	raw, err := os.ReadFile(whisperSRT)
	if err != nil {
		t.Fatalf("read whisper srt: %v", err)
	}
	final, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read output srt: %v", err)
	}
	if !bytes.Equal(raw, final) {
		t.Fatalf("expected fallback output to match whisper srt")
	}
}
