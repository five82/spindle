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
	"syscall"
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

// Params holds the fields New needs from config.SubtitlesConfig's WhisperX-
// prefixed settings.
type Params struct {
	Model       string
	CUDAEnabled bool
	VADMethod   string
	HFToken     string
}

// New creates a transcription service.
func New(p Params, logger *slog.Logger) *Service {
	logger = logs.Default(logger)
	model := p.Model
	if model == "" {
		model = "large-v3"
	}
	vadMethod := p.VADMethod
	if vadMethod == "" {
		vadMethod = "silero"
	}
	return &Service{
		model:       model,
		cudaEnabled: p.CUDAEnabled,
		vadMethod:   vadMethod,
		hfToken:     p.HFToken,
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
	ItemID     int64
	EpisodeKey string
	Purpose    string
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

// ConfigureGroupKill runs cmd in its own process group, kills the WHOLE
// group on context cancellation, and caps the post-kill pipe-drain wait.
// uvx spawns python as a grandchild that inherits our pipes: killing only
// uvx leaves an orphaned python holding the GPU with the pipe open, which
// blocks CombinedOutput -- and therefore daemon shutdown -- until the
// orphan finishes on its own.
func ConfigureGroupKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second
}

type whisperXInvocation struct {
	Args                     []string
	Env                      []string
	Device                   string
	ComputeType              string
	ConditionOnPreviousText  bool
	TranscriptionProfileName string
}

// Transcribe runs WhisperX transcription for a single request. It is a batch
// of one; see TranscribeBatch.
func (s *Service) Transcribe(ctx context.Context, req TranscribeRequest, progress ...ProgressFunc) (*TranscribeResult, error) {
	results, err := s.TranscribeBatch(ctx, []TranscribeRequest{req}, progress...)
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// TranscribeBatch runs WhisperX transcription for several requests in ONE
// wrapper invocation, so uvx resolution, torch import, and model load are
// paid once per batch instead of once per file. Every request must resolve
// to the same model; languages may vary (the wrapper caches per-language
// models). Results are returned in request order. A failure of any request
// fails the whole batch, matching the previous serial-loop behavior where
// the first failure aborted the stage.
//
// Steps:
//  1. Extract each request's audio via FFmpeg.
//  2. Run WhisperX once via uvx over all extracted files.
//  3. Read each SRT, count segments, parse duration.
//
// If progress is non-nil, it is called at the start and end of each phase
// (once per phase for the whole batch).
func (s *Service) TranscribeBatch(ctx context.Context, reqs []TranscribeRequest, progress ...ProgressFunc) ([]*TranscribeResult, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("transcribe batch: no requests")
	}
	var onProgress ProgressFunc
	if len(progress) > 0 {
		onProgress = progress[0]
	}

	model := reqs[0].Model
	if model == "" {
		model = s.model
	}
	for _, req := range reqs[1:] {
		m := req.Model
		if m == "" {
			m = s.model
		}
		if m != model {
			return nil, fmt.Errorf("transcribe batch: mixed models %q and %q", model, m)
		}
	}

	// Extract audio for every request via FFmpeg.
	if onProgress != nil {
		onProgress(PhaseExtract, 0)
	}
	extractStart := time.Now()
	wavPaths := make([]string, len(reqs))
	for i, req := range reqs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
			return nil, fmt.Errorf("create output dir: %w", err)
		}
		wavPath := filepath.Join(req.OutputDir, "audio.wav")
		wavPaths[i] = wavPath
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
			transcriptionLogFields(req,
				"event_type", "transcription_extract",
				"input", req.InputPath,
				"output_dir", req.OutputDir,
			)...,
		)
		ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
		if output, err := ffmpegCmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg audio extraction (%s): %w: %s", req.InputPath, err, output)
		}
	}
	extractTime := time.Since(extractStart)
	if onProgress != nil {
		onProgress(PhaseExtract, extractTime)
	}

	// Run WhisperX once via the embedded wrapper.
	if onProgress != nil {
		onProgress(PhaseTranscribe, 0)
	}
	invocation := s.buildWhisperXInvocation(wavPaths, reqs, model)
	s.logger.Info("running WhisperX transcription",
		transcriptionLogFields(reqs[0],
			"event_type", "transcription_whisperx",
			"decision_type", "transcription_profile",
			"decision_result", invocation.TranscriptionProfileName,
			"decision_reason", fmt.Sprintf("vad_method=%s device=%s compute_type=%s condition_on_previous_text=%t batch_size=%d chunk_size=%d", s.vadMethod, invocation.Device, invocation.ComputeType, invocation.ConditionOnPreviousText, whisperXBatchSize, whisperXVADChunkSize),
			"model", model,
			"batch_files", len(reqs),
		)...,
	)
	transcribeStart := time.Now()
	whisperCmd := exec.CommandContext(ctx, whisperXCommand, invocation.Args...)
	whisperCmd.Env = invocation.Env
	ConfigureGroupKill(whisperCmd)
	if output, err := whisperCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisperx transcription: %w: %s", err, output)
	}
	transcribeTime := time.Since(transcribeStart)
	if onProgress != nil {
		onProgress(PhaseTranscribe, transcribeTime)
	}

	// Collect canonical WhisperX outputs per request.
	results := make([]*TranscribeResult, len(reqs))
	for i, req := range reqs {
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

		results[i] = &TranscribeResult{
			SRTPath:        srtPath,
			JSONPath:       jsonPath,
			Duration:       duration,
			Segments:       segments,
			ExtractTime:    extractTime,
			TranscribeTime: transcribeTime,
		}

		s.logger.Info("WhisperX transcription completed",
			transcriptionLogFields(req,
				"event_type", "transcription_whisperx_complete",
				"segments", segments,
				"content_duration_s", duration,
				"duration_ms", transcribeTime.Milliseconds(),
				"srt_path", srtPath,
				"json_path", jsonPath,
			)...,
		)
	}

	return results, nil
}

func transcriptionLogFields(req TranscribeRequest, fields ...any) []any {
	out := make([]any, 0, len(fields)+8)
	if req.ItemID != 0 {
		out = append(out, "item_id", req.ItemID)
	}
	if req.EpisodeKey != "" {
		out = append(out, "episode_key", req.EpisodeKey)
	}
	if req.Purpose != "" {
		out = append(out, "purpose", req.Purpose)
	}
	out = append(out, "audio_index", req.AudioIndex)
	return append(out, fields...)
}

func (s *Service) buildWhisperXInvocation(wavPaths []string, reqs []TranscribeRequest, model string) whisperXInvocation {
	device := "cpu"
	computeType := "int8"
	if s.cudaEnabled {
		device = "cuda"
		computeType = "float16"
	}
	args := []string{
		"--from", whisperXPackage,
		"python", "-c", whisperXWrapperScript,
	}
	// --audio/--output-dir/--language repeat together, one triple per request.
	for i, req := range reqs {
		args = append(args,
			"--audio", wavPaths[i],
			"--output-dir", req.OutputDir,
			"--language", req.Language,
		)
	}
	args = append(args,
		"--model", model,
		"--vad-method", s.vadMethod,
		"--device", device,
		"--compute-type", computeType,
		"--batch-size", strconv.Itoa(whisperXBatchSize),
		"--chunk-size", strconv.Itoa(whisperXVADChunkSize),
		"--vad-onset", fmt.Sprintf("%.3f", whisperXVADOnset),
		"--vad-offset", fmt.Sprintf("%.3f", whisperXVADOffset),
		"--condition-on-previous-text", "false",
		"--transcription-profile-name", transcriptionProfileID,
	)
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
