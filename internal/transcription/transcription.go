// Package transcription provides shared canonical WhisperX transcription.
package transcription

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/srtutil"
)

// Service provides WhisperX transcription.
type Service struct {
	model       string
	cudaEnabled bool
	vadMethod   string
	hfToken     string
	logger      *slog.Logger
}

// New creates a transcription service.
func New(model string, cudaEnabled bool, vadMethod, hfToken string, logger *slog.Logger) *Service {
	logger = logs.Default(logger)
	if model == "" {
		model = "large-v3"
	}
	if vadMethod == "" {
		vadMethod = "silero"
	}
	return &Service{
		model:       model,
		cudaEnabled: cudaEnabled,
		vadMethod:   vadMethod,
		hfToken:     hfToken,
		logger:      logger,
	}
}

// TranscribeRequest specifies what to transcribe.
type TranscribeRequest struct {
	InputPath  string
	AudioIndex int // audio-relative index (maps to ffmpeg 0:a:N)
	Language   string
	OutputDir  string
	Model      string // Override default model
}

// Phase identifies a transcription progress phase.
type Phase string

const (
	PhaseExtract    Phase = "extract"
	PhaseTranscribe Phase = "transcribe"
)

// ProgressFunc is called at phase boundaries during transcription.
// phase is the current transcription phase. elapsed is zero at phase start
// and non-zero at phase end.
type ProgressFunc func(phase Phase, elapsed time.Duration)

// TranscribeResult contains canonical transcription output.
type TranscribeResult struct {
	SRTPath        string
	JSONPath       string
	Duration       float64 // transcript-tail duration from last SRT cue timestamp
	Segments       int
	ExtractTime    time.Duration // time spent on ffmpeg audio extraction
	TranscribeTime time.Duration // time spent on WhisperX
}

type whisperXInvocation struct {
	Args                     []string
	Env                      []string
	Device                   string
	ComputeType              string
	ConditionOnPreviousText  bool
	TranscriptionProfileName string
}

// Transcribe runs WhisperX transcription.
//
// Steps:
//  1. Extract audio via FFmpeg.
//  2. Run WhisperX via uvx.
//  3. Read SRT, count segments, parse duration.
//  4. Return result.
//
// If progress is non-nil, it is called at the start and end of each phase.
func (s *Service) Transcribe(ctx context.Context, req TranscribeRequest, progress ...ProgressFunc) (*TranscribeResult, error) {
	var onProgress ProgressFunc
	if len(progress) > 0 {
		onProgress = progress[0]
	}

	model := req.Model
	if model == "" {
		model = s.model
	}

	// Ensure output directory exists.
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Extract audio via FFmpeg.
	if onProgress != nil {
		onProgress(PhaseExtract, 0)
	}
	wavPath := filepath.Join(req.OutputDir, "audio.wav")
	ffmpegArgs := []string{
		"-i", req.InputPath,
		"-map", fmt.Sprintf("0:a:%d", req.AudioIndex),
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-vn", "-sn", "-dn",
		"-y",
		wavPath,
	}

	s.logger.Info("extracting audio for transcription",
		"event_type", "transcription_extract",
		"input", req.InputPath,
		"audio_index", req.AudioIndex,
	)
	extractStart := time.Now()
	ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
	if output, err := ffmpegCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg audio extraction: %w: %s", err, output)
	}
	extractTime := time.Since(extractStart)
	if onProgress != nil {
		onProgress(PhaseExtract, extractTime)
	}

	// Run WhisperX via the embedded wrapper.
	if onProgress != nil {
		onProgress(PhaseTranscribe, 0)
	}
	invocation := s.buildWhisperXInvocation(wavPath, req.OutputDir, model, req.Language)
	s.logger.Info("running WhisperX transcription",
		"event_type", "transcription_whisperx",
		"decision_type", "transcription_profile",
		"decision_result", invocation.TranscriptionProfileName,
		"decision_reason", fmt.Sprintf("vad_method=%s device=%s compute_type=%s condition_on_previous_text=%t batch_size=%d chunk_size=%d", s.vadMethod, invocation.Device, invocation.ComputeType, invocation.ConditionOnPreviousText, whisperXBatchSize, whisperXVADChunkSize),
		"model", model,
		"language", req.Language,
	)
	transcribeStart := time.Now()
	whisperCmd := exec.CommandContext(ctx, whisperXCommand, invocation.Args...)
	whisperCmd.Env = invocation.Env
	if output, err := whisperCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisperx transcription: %w: %s", err, output)
	}
	transcribeTime := time.Since(transcribeStart)
	if onProgress != nil {
		onProgress(PhaseTranscribe, transcribeTime)
	}

	// Find canonical WhisperX outputs. WhisperX names them after the input wav.
	srtPath := filepath.Join(req.OutputDir, "audio.srt")
	if _, err := os.Stat(srtPath); err != nil {
		return nil, fmt.Errorf("srt output not found at %s: %w", srtPath, err)
	}
	jsonPath := filepath.Join(req.OutputDir, "audio.json")
	if _, err := os.Stat(jsonPath); err != nil {
		return nil, fmt.Errorf("json output not found at %s: %w", jsonPath, err)
	}

	segments, duration, err := analyzeSRT(srtPath)
	if err != nil {
		return nil, fmt.Errorf("analyze srt: %w", err)
	}

	result := &TranscribeResult{
		SRTPath:        srtPath,
		JSONPath:       jsonPath,
		Duration:       duration,
		Segments:       segments,
		ExtractTime:    extractTime,
		TranscribeTime: transcribeTime,
	}

	return result, nil
}

func (s *Service) buildWhisperXInvocation(wavPath, outputDir, model, language string) whisperXInvocation {
	device := "cpu"
	computeType := "int8"
	if s.cudaEnabled {
		device = "cuda"
		computeType = "float16"
	}
	args := []string{
		"--from", whisperXPackage,
		"python", "-c", whisperXWrapperScript,
		"--audio", wavPath,
		"--output-dir", outputDir,
		"--model", model,
		"--language", language,
		"--vad-method", s.vadMethod,
		"--device", device,
		"--compute-type", computeType,
		"--batch-size", strconv.Itoa(whisperXBatchSize),
		"--chunk-size", strconv.Itoa(whisperXVADChunkSize),
		"--vad-onset", fmt.Sprintf("%.3f", whisperXVADOnset),
		"--vad-offset", fmt.Sprintf("%.3f", whisperXVADOffset),
		"--condition-on-previous-text", "false",
		"--transcription-profile-name", transcriptionProfileID,
	}
	env := append(os.Environ(), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	if s.hfToken != "" {
		args = append(args, "--hf-token", s.hfToken)
		env = append(env, "HUGGING_FACE_HUB_TOKEN="+s.hfToken, "HF_TOKEN="+s.hfToken)
	}
	return whisperXInvocation{
		Args:                     args,
		Env:                      env,
		Device:                   device,
		ComputeType:              computeType,
		ConditionOnPreviousText:  false,
		TranscriptionProfileName: transcriptionProfileID,
	}
}

// analyzeSRT reads an SRT file once and returns both the segment count and
// the duration (end timestamp of the last cue, in seconds).
func analyzeSRT(path string) (segments int, duration float64, err error) {
	cues, err := srtutil.ParseFile(path)
	if err != nil {
		return 0, 0, err
	}
	if len(cues) == 0 {
		return 0, 0, nil
	}
	return len(cues), cues[len(cues)-1].End, nil
}

// Config returns the service's WhisperX configuration for display purposes.
func (s *Service) Config() (model, device, vadMethod string) {
	model = s.model
	if s.cudaEnabled {
		device = "cuda"
	} else {
		device = "cpu"
	}
	vadMethod = s.vadMethod
	return
}
