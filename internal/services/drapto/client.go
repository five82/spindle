package drapto

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var commandContext = exec.CommandContext

// EventType enumerates Drapto reporter payloads.
type EventType string

const (
	EventTypeUnknown           EventType = ""
	EventTypeHardware          EventType = "hardware"
	EventTypeInitialization    EventType = "initialization"
	EventTypeStageProgress     EventType = "stage_progress"
	EventTypeEncodingStarted   EventType = "encoding_started"
	EventTypeEncodingProgress  EventType = "encoding_progress"
	EventTypeEncodingConfig    EventType = "encoding_config"
	EventTypeCropResult        EventType = "crop_result"
	EventTypeValidation        EventType = "validation_complete"
	EventTypeEncodingComplete  EventType = "encoding_complete"
	EventTypeWarning           EventType = "warning"
	EventTypeError             EventType = "error"
	EventTypeOperationComplete EventType = "operation_complete"
	EventTypeBatchStarted      EventType = "batch_started"
	EventTypeFileProgress      EventType = "file_progress"
	EventTypeBatchComplete     EventType = "batch_complete"
)

// ProgressUpdate captures Drapto progress events.
type ProgressUpdate struct {
	Type              EventType
	Percent           float64
	Stage             string
	Message           string
	ETA               time.Duration
	Speed             float64
	FPS               float64
	Bitrate           string
	Timestamp         time.Time
	TotalFrames       int64
	CurrentFrame      int64
	Hardware          *HardwareInfo
	Video             *VideoInfo
	Crop              *CropSummary
	EncodingConfig    *EncodingConfig
	Validation        *ValidationSummary
	Result            *EncodingResult
	Warning           string
	Error             *ReporterIssue
	OperationComplete string
	BatchStart        *BatchStartInfo
	FileProgress      *FileProgress
	BatchSummary      *BatchSummary
}

// HardwareInfo mirrors Drapto's host summary event.
type HardwareInfo struct {
	Hostname string
}

// VideoInfo mirrors the initialization summary event.
type VideoInfo struct {
	InputFile        string
	OutputFile       string
	Duration         string
	Resolution       string
	Category         string
	DynamicRange     string
	AudioDescription string
}

// CropSummary relays the crop detection result.
type CropSummary struct {
	Message  string
	Crop     string
	Required bool
	Disabled bool
}

// EncodingConfig captures the encoder configuration for the current file.
type EncodingConfig struct {
	Encoder            string
	Preset             string
	Tune               string
	Quality            string
	PixelFormat        string
	MatrixCoefficients string
	AudioCodec         string
	AudioDescription   string
	DraptoPreset       string
	PresetSettings     []PresetSetting
	SVTParams          string
}

// PresetSetting exposes a key/value pair from a Drapto preset.
type PresetSetting struct {
	Key   string
	Value string
}

// ValidationSummary includes per-step validation outcomes.
type ValidationSummary struct {
	Passed bool
	Steps  []ValidationStep
}

// ValidationStep captures a single validation check.
type ValidationStep struct {
	Name    string
	Passed  bool
	Details string
}

// EncodingResult mirrors Drapto's encoding_complete payload.
type EncodingResult struct {
	InputFile            string
	OutputFile           string
	OriginalSize         int64
	EncodedSize          int64
	VideoStream          string
	AudioStream          string
	AverageSpeed         float64
	OutputPath           string
	Duration             time.Duration
	SizeReductionPercent float64
}

// ReporterIssue captures Drapto warning/error metadata.
type ReporterIssue struct {
	Title      string
	Message    string
	Context    string
	Suggestion string
}

// BatchStartInfo mirrors Drapto's batch_started event.
type BatchStartInfo struct {
	TotalFiles int
	FileList   []string
	OutputDir  string
}

// FileProgress exposes batch file counters.
type FileProgress struct {
	CurrentFile int
	TotalFiles  int
}

// BatchSummary exposes aggregate batch outcomes.
type BatchSummary struct {
	SuccessfulCount       int
	TotalFiles            int
	TotalOriginalSize     int64
	TotalEncodedSize      int64
	TotalDuration         time.Duration
	TotalReductionPercent float64
}

// Client defines Drapto encoding behaviour.
type Client interface {
	Encode(ctx context.Context, inputPath, outputDir string, opts EncodeOptions) (string, error)
}

// EncodeOptions configures how the encoder should run.
type EncodeOptions struct {
	Progress      func(ProgressUpdate)
	PresetProfile string
}

// Option configures the CLI client.
type Option func(*CLI)

// WithBinary overrides the default binary name.
func WithBinary(binary string) Option {
	return func(c *CLI) {
		if binary != "" {
			c.binary = binary
		}
	}
}

// CLI wraps the drapto command-line encoder.
type CLI struct {
	binary string
}

// NewCLI constructs a CLI client using defaults.
func NewCLI(opts ...Option) *CLI {
	cli := &CLI{binary: "drapto"}
	for _, opt := range opts {
		opt(cli)
	}
	return cli
}

// Encode launches drapto encode and returns the output path.
func (c *CLI) Encode(ctx context.Context, inputPath, outputDir string, opts EncodeOptions) (string, error) {
	if inputPath == "" {
		return "", errors.New("input path required")
	}
	if outputDir == "" {
		return "", errors.New("output directory required")
	}

	cleanOutputDir := strings.TrimSpace(outputDir)
	if cleanOutputDir == "" {
		return "", errors.New("output directory required")
	}

	base := filepath.Base(inputPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
	outputPath := filepath.Join(cleanOutputDir, stem+".mkv")

	args := []string{
		"encode",
		"--input", inputPath,
		"--output", cleanOutputDir,
		"--responsive",
		"--no-log",
	}
	if profile := strings.TrimSpace(opts.PresetProfile); profile != "" && !strings.EqualFold(profile, "default") {
		args = append(args, "--drapto-preset", profile)
	}
	args = append(args, "--progress-json")
	cmd := commandContext(ctx, c.binary, args...) //nolint:gosec
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start drapto: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		update, ok := parseProgressEvent(scanner.Bytes())
		if !ok {
			continue
		}
		if opts.Progress != nil {
			opts.Progress(update)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read drapto output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("drapto encode failed: %w", err)
	}

	return outputPath, nil
}

var _ Client = (*CLI)(nil)

type jsonEvent struct {
	Type                 string   `json:"type"`
	Percent              *float64 `json:"percent"`
	Stage                string   `json:"stage"`
	Message              string   `json:"message"`
	ETASeconds           *float64 `json:"eta_seconds"`
	Speed                *float64 `json:"speed"`
	FPS                  *float64 `json:"fps"`
	Bitrate              string   `json:"bitrate"`
	Timestamp            *int64   `json:"timestamp"`
	Hostname             string   `json:"hostname"`
	InputFile            string   `json:"input_file"`
	OutputFile           string   `json:"output_file"`
	Duration             string   `json:"duration"`
	Resolution           string   `json:"resolution"`
	Category             string   `json:"category"`
	DynamicRange         string   `json:"dynamic_range"`
	AudioDescription     string   `json:"audio_description"`
	Crop                 string   `json:"crop"`
	Required             *bool    `json:"required"`
	Disabled             *bool    `json:"disabled"`
	Encoder              string   `json:"encoder"`
	Preset               string   `json:"preset"`
	Tune                 string   `json:"tune"`
	Quality              string   `json:"quality"`
	PixelFormat          string   `json:"pixel_format"`
	MatrixCoefficients   string   `json:"matrix_coefficients"`
	AudioCodec           string   `json:"audio_codec"`
	DraptoPreset         string   `json:"drapto_preset"`
	DraptoPresetSettings []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"drapto_preset_settings"`
	SVTParams        string `json:"svtav1_params"`
	ValidationPassed *bool  `json:"validation_passed"`
	ValidationSteps  []struct {
		Step    string `json:"step"`
		Passed  bool   `json:"passed"`
		Details string `json:"details"`
	} `json:"validation_steps"`
	Title                     string   `json:"title"`
	Context                   string   `json:"context"`
	Suggestion                string   `json:"suggestion"`
	OriginalSize              *int64   `json:"original_size"`
	EncodedSize               *int64   `json:"encoded_size"`
	VideoStream               string   `json:"video_stream"`
	AudioStream               string   `json:"audio_stream"`
	AverageSpeed              *float64 `json:"average_speed"`
	OutputPath                string   `json:"output_path"`
	DurationSeconds           *int64   `json:"duration_seconds"`
	SizeReductionPercent      *float64 `json:"size_reduction_percent"`
	TotalFrames               *int64   `json:"total_frames"`
	CurrentFrame              *int64   `json:"current_frame"`
	FileList                  []string `json:"file_list"`
	OutputDir                 string   `json:"output_dir"`
	SuccessfulCount           *int     `json:"successful_count"`
	TotalFiles                *int     `json:"total_files"`
	TotalOriginalSize         *int64   `json:"total_original_size"`
	TotalEncodedSize          *int64   `json:"total_encoded_size"`
	TotalDurationSeconds      *int64   `json:"total_duration_seconds"`
	TotalSizeReductionPercent *float64 `json:"total_size_reduction_percent"`
	CurrentFile               *int     `json:"current_file"`
}

func parseProgressEvent(line []byte) (ProgressUpdate, bool) {
	var payload jsonEvent
	if err := json.Unmarshal(line, &payload); err != nil {
		return ProgressUpdate{}, false
	}
	update := ProgressUpdate{
		Type:     EventType(payload.Type),
		Percent:  -1,
		Stage:    strings.TrimSpace(payload.Stage),
		Message:  payload.Message,
		Bitrate:  payload.Bitrate,
		Hardware: nil,
	}
	if payload.Timestamp != nil {
		update.Timestamp = time.Unix(*payload.Timestamp, 0).UTC()
	}
	if payload.Percent != nil {
		update.Percent = *payload.Percent
	}
	if payload.ETASeconds != nil {
		update.ETA = time.Duration(*payload.ETASeconds * float64(time.Second))
	}
	if payload.Speed != nil {
		update.Speed = *payload.Speed
	}
	if payload.FPS != nil {
		update.FPS = *payload.FPS
	}
	if payload.TotalFrames != nil {
		update.TotalFrames = *payload.TotalFrames
	}
	if payload.CurrentFrame != nil {
		update.CurrentFrame = *payload.CurrentFrame
	}
	switch update.Type {
	case EventTypeHardware:
		update.Hardware = &HardwareInfo{Hostname: payload.Hostname}
	case EventTypeInitialization:
		update.Video = &VideoInfo{
			InputFile:        payload.InputFile,
			OutputFile:       payload.OutputFile,
			Duration:         payload.Duration,
			Resolution:       payload.Resolution,
			Category:         payload.Category,
			DynamicRange:     payload.DynamicRange,
			AudioDescription: payload.AudioDescription,
		}
	case EventTypeCropResult:
		update.Crop = &CropSummary{
			Message:  payload.Message,
			Crop:     payload.Crop,
			Required: payload.Required != nil && *payload.Required,
			Disabled: payload.Disabled != nil && *payload.Disabled,
		}
	case EventTypeEncodingConfig:
		settings := make([]PresetSetting, 0, len(payload.DraptoPresetSettings))
		for _, setting := range payload.DraptoPresetSettings {
			settings = append(settings, PresetSetting{Key: setting.Key, Value: setting.Value})
		}
		update.EncodingConfig = &EncodingConfig{
			Encoder:            payload.Encoder,
			Preset:             payload.Preset,
			Tune:               payload.Tune,
			Quality:            payload.Quality,
			PixelFormat:        payload.PixelFormat,
			MatrixCoefficients: payload.MatrixCoefficients,
			AudioCodec:         payload.AudioCodec,
			AudioDescription:   payload.AudioDescription,
			DraptoPreset:       payload.DraptoPreset,
			PresetSettings:     settings,
			SVTParams:          payload.SVTParams,
		}
	case EventTypeValidation:
		steps := make([]ValidationStep, 0, len(payload.ValidationSteps))
		for _, step := range payload.ValidationSteps {
			steps = append(steps, ValidationStep{Name: step.Step, Passed: step.Passed, Details: step.Details})
		}
		update.Validation = &ValidationSummary{
			Passed: payload.ValidationPassed != nil && *payload.ValidationPassed,
			Steps:  steps,
		}
	case EventTypeEncodingComplete:
		var duration time.Duration
		if payload.DurationSeconds != nil {
			duration = time.Duration(*payload.DurationSeconds) * time.Second
		}
		var reduction float64
		if payload.SizeReductionPercent != nil {
			reduction = *payload.SizeReductionPercent
		}
		update.Result = &EncodingResult{
			InputFile:            payload.InputFile,
			OutputFile:           payload.OutputFile,
			OriginalSize:         valueOrZero(payload.OriginalSize),
			EncodedSize:          valueOrZero(payload.EncodedSize),
			VideoStream:          payload.VideoStream,
			AudioStream:          payload.AudioStream,
			AverageSpeed:         valueFloatOrZero(payload.AverageSpeed),
			OutputPath:           payload.OutputPath,
			Duration:             duration,
			SizeReductionPercent: reduction,
		}
	case EventTypeWarning:
		update.Warning = payload.Message
	case EventTypeError:
		update.Error = &ReporterIssue{
			Title:      payload.Title,
			Message:    payload.Message,
			Context:    payload.Context,
			Suggestion: payload.Suggestion,
		}
	case EventTypeOperationComplete:
		update.OperationComplete = payload.Message
	case EventTypeBatchStarted:
		update.BatchStart = &BatchStartInfo{
			TotalFiles: valueIntOrZero(payload.TotalFiles),
			FileList:   append([]string(nil), payload.FileList...),
			OutputDir:  payload.OutputDir,
		}
	case EventTypeFileProgress:
		update.FileProgress = &FileProgress{
			CurrentFile: valueIntOrZero(payload.CurrentFile),
			TotalFiles:  valueIntOrZero(payload.TotalFiles),
		}
	case EventTypeBatchComplete:
		var totalDuration time.Duration
		if payload.TotalDurationSeconds != nil {
			totalDuration = time.Duration(*payload.TotalDurationSeconds) * time.Second
		}
		var reduction float64
		if payload.TotalSizeReductionPercent != nil {
			reduction = *payload.TotalSizeReductionPercent
		}
		update.BatchSummary = &BatchSummary{
			SuccessfulCount:       valueIntOrZero(payload.SuccessfulCount),
			TotalFiles:            valueIntOrZero(payload.TotalFiles),
			TotalOriginalSize:     valueOrZero(payload.TotalOriginalSize),
			TotalEncodedSize:      valueOrZero(payload.TotalEncodedSize),
			TotalDuration:         totalDuration,
			TotalReductionPercent: reduction,
		}
	}
	return update, true
}

func valueOrZero(val *int64) int64 {
	if val == nil {
		return 0
	}
	return *val
}

func valueIntOrZero(val *int) int {
	if val == nil {
		return 0
	}
	return *val
}

func valueFloatOrZero(val *float64) float64 {
	if val == nil {
		return 0
	}
	return *val
}
