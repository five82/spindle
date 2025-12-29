package subtitles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/config"
)

func TestServiceGenerateRequiresTokenForPyannote(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 30, false)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.WhisperXVADMethod = "pyannote"
	cfg.Subtitles.WhisperXHuggingFace = ""
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	if _, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	}); err == nil || !strings.Contains(err.Error(), "Hugging Face token") {
		t.Fatalf("expected configuration error about Hugging Face token, got %v", err)
	}
}

func TestServiceGeneratePyannoteWithToken(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 45, false)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.WhisperXVADMethod = "pyannote"
	cfg.Subtitles.WhisperXHuggingFace = "token"
	validator := func(ctx context.Context, token string) (tokenValidationResult, error) {
		if token != "token" {
			return tokenValidationResult{}, fmt.Errorf("unexpected token: %s", token)
		}
		return tokenValidationResult{Account: "pixar-studios"}, nil
	}
	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithTokenValidator(validator),
		WithoutDependencyCheck(),
	)

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
	if stub.lastVAD != whisperXVADMethodPyannote {
		t.Fatalf("expected VAD %q, got %q", whisperXVADMethodPyannote, stub.lastVAD)
	}
	if stub.lastHFToken != "token" {
		t.Fatalf("expected hf token to be passed to whisperx")
	}
}

func TestServiceGeneratePyannoteTokenFallbackToSilero(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 45, false)

	cfg := config.Default()
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.WhisperXVADMethod = "pyannote"
	cfg.Subtitles.WhisperXHuggingFace = "bad-token"
	validator := func(ctx context.Context, token string) (tokenValidationResult, error) {
		if token != "bad-token" {
			return tokenValidationResult{}, fmt.Errorf("unexpected token: %s", token)
		}
		return tokenValidationResult{}, fmt.Errorf("%w: test rejection", errPyannoteUnauthorized)
	}
	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithTokenValidator(validator),
		WithoutDependencyCheck(),
	)

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected fallback subtitles to contain segments")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if stub.lastVAD != whisperXVADMethodSilero {
		t.Fatalf("expected fallback VAD %q, got %q", whisperXVADMethodSilero, stub.lastVAD)
	}
	if stub.lastHFToken != "" {
		t.Fatalf("expected no HF token when falling back, got %q", stub.lastHFToken)
	}
}
