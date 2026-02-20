package whisperx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	langpkg "spindle/internal/language"
)

// Service provides WhisperX transcription capabilities.
type Service struct {
	cfg           Config
	ffmpegBinary  string
	commandRunner func(ctx context.Context, name string, args ...string) error
}

// NewService creates a WhisperX service with the given configuration.
func NewService(cfg Config, ffmpegBinary string) *Service {
	if ffmpegBinary == "" {
		ffmpegBinary = FFmpegCommand
	}
	return &Service{
		cfg:          cfg,
		ffmpegBinary: ffmpegBinary,
	}
}

// WithCommandRunner sets a custom command runner (for testing).
func (s *Service) WithCommandRunner(runner func(ctx context.Context, name string, args ...string) error) {
	s.commandRunner = runner
}

// SetVADMethod updates the VAD method at runtime (used when HF token validation fails).
func (s *Service) SetVADMethod(method string) {
	s.cfg.VADMethod = method
}

// Model returns the configured model name for logging.
func (s *Service) Model() string {
	if s.cfg.Model != "" {
		return s.cfg.Model
	}
	return DefaultModel
}

// CUDAEnabled returns whether CUDA is enabled.
func (s *Service) CUDAEnabled() bool {
	return s.cfg.CUDAEnabled
}

// ExtractFullAudio extracts the entire audio stream from a source file.
// The output is a mono 16kHz WAV file suitable for WhisperX.
// This method uses the service's command runner if configured.
func (s *Service) ExtractFullAudio(ctx context.Context, source string, audioIndex int, dest string) error {
	if s.commandRunner != nil {
		args := buildFFmpegExtractArgs(source, audioIndex, -1, -1, dest)
		return s.commandRunner(ctx, s.ffmpegBinary, args...)
	}
	return ExtractFullAudio(ctx, s.ffmpegBinary, source, audioIndex, dest)
}

// ExtractSegment extracts a time-range segment of audio from a source file.
// This method uses the service's command runner if configured.
func (s *Service) ExtractSegment(ctx context.Context, source string, audioIndex int, startSec, durationSec int, dest string) error {
	if s.commandRunner != nil {
		args := buildFFmpegExtractArgs(source, audioIndex, startSec, durationSec, dest)
		return s.commandRunner(ctx, s.ffmpegBinary, args...)
	}
	return ExtractSegment(ctx, s.ffmpegBinary, source, audioIndex, startSec, durationSec, dest)
}

// run executes a command, using the custom runner if set.
func (s *Service) run(ctx context.Context, name string, args ...string) error {
	if s.commandRunner != nil {
		return s.commandRunner(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec

	// Torch 2.6 changed torch.load default to weights_only=true, breaking WhisperX/pyannote.
	// Force legacy behavior so bundled WhisperX binaries can load checkpoints safely.
	if os.Getenv("TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD") == "" {
		cmd.Env = append(os.Environ(), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// TranscribeResult contains the result of a transcription.
type TranscribeResult struct {
	// Text is the plain text transcription.
	Text string
	// SRTPath is the path to the generated SRT file (if available).
	SRTPath string
	// JSONPath is the path to the generated JSON file (if available).
	JSONPath string
}

// TranscribeFile transcribes an audio file and returns the text.
// The source should be a WAV file extracted for WhisperX.
// outputDir is where WhisperX will write its output files.
func (s *Service) TranscribeFile(ctx context.Context, source, outputDir, language string) (TranscribeResult, error) {
	var result TranscribeResult

	if source == "" {
		return result, fmt.Errorf("transcribe: source path required")
	}
	if outputDir == "" {
		outputDir = filepath.Dir(source)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return result, fmt.Errorf("transcribe: ensure output dir: %w", err)
	}

	args := s.buildArgs(source, outputDir, language)
	if err := s.run(ctx, UVXCommand, args...); err != nil {
		return result, fmt.Errorf("whisperx: %w", err)
	}

	// Derive output file paths from source
	baseName := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	result.SRTPath = filepath.Join(outputDir, baseName+".srt")
	result.JSONPath = filepath.Join(outputDir, baseName+".json")

	// Load transcript text from JSON
	if text, err := loadTranscriptText(result.JSONPath); err == nil {
		result.Text = text
	}

	return result, nil
}

// TranscribeSegment extracts and transcribes a segment of audio.
// startSec is the start time in seconds, durationSec is the segment length.
// workDir is a working directory for temporary files.
func (s *Service) TranscribeSegment(ctx context.Context, source string, audioIndex int, startSec, durationSec int, workDir, language string) (TranscribeResult, error) {
	var result TranscribeResult

	if workDir == "" {
		return result, fmt.Errorf("transcribe segment: workDir required")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return result, fmt.Errorf("transcribe segment: ensure workDir: %w", err)
	}

	// Extract segment
	segmentPath := filepath.Join(workDir, fmt.Sprintf("segment_%d_%d.wav", startSec, durationSec))
	if err := ExtractSegment(ctx, s.ffmpegBinary, source, audioIndex, startSec, durationSec, segmentPath); err != nil {
		return result, fmt.Errorf("transcribe segment: extract: %w", err)
	}

	// Transcribe
	return s.TranscribeFile(ctx, segmentPath, workDir, language)
}

// buildArgs constructs the uvx command arguments for WhisperX.
func (s *Service) buildArgs(source, outputDir, language string) []string {
	args := make([]string, 0, 32)

	// Index URLs
	if s.cfg.CUDAEnabled {
		args = append(args,
			"--index-url", CUDAIndexURL,
			"--extra-index-url", PypiIndexURL,
		)
	} else {
		args = append(args, "--index-url", PypiIndexURL)
	}

	// Model
	model := s.cfg.Model
	if model == "" {
		model = DefaultModel
	}

	args = append(args,
		"whisperx",
		source,
		"--model", model,
		"--batch_size", BatchSize,
		"--output_dir", outputDir,
		"--output_format", OutputFormat,
		"--segment_resolution", SegmentResolution,
		"--chunk_size", ChunkSize,
		"--vad_onset", VADOnset,
		"--vad_offset", VADOffset,
		"--beam_size", BeamSize,
		"--best_of", BestOf,
		"--temperature", Temperature,
		"--patience", Patience,
	)

	// VAD method
	vadMethod := s.cfg.VADMethod
	if vadMethod == "" {
		vadMethod = VADMethodSilero
	}
	args = append(args, "--vad_method", vadMethod)
	if vadMethod == VADMethodPyannote && s.cfg.HFToken != "" {
		args = append(args, "--hf_token", s.cfg.HFToken)
	}

	// Language
	if lang := langpkg.ToISO2(language); lang != "" {
		args = append(args, "--language", lang)
	}

	// Device
	if s.cfg.CUDAEnabled {
		args = append(args, "--device", CUDADevice)
	} else {
		args = append(args, "--device", CPUDevice, "--compute_type", CPUComputeType)
	}

	return args
}

// Word represents a single word with timing from WhisperX output.
type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Segment represents a transcribed segment from WhisperX JSON output.
type Segment struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Words []Word  `json:"words"`
}

// whisperXPayload is the JSON structure from WhisperX output.
type whisperXPayload struct {
	Segments []Segment `json:"segments"`
}

// LoadSegments loads segments from a WhisperX JSON file.
func LoadSegments(jsonPath string) ([]Segment, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var payload whisperXPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse whisperx json: %w", err)
	}
	return payload.Segments, nil
}

// loadTranscriptText loads and concatenates text from a WhisperX JSON file.
func loadTranscriptText(jsonPath string) (string, error) {
	segments, err := LoadSegments(jsonPath)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, seg := range segments {
		if text := strings.TrimSpace(seg.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " "), nil
}
