package drapto

import (
	"context"
	"time"
)

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

// CropCandidate represents a detected crop value and its frequency.
type CropCandidate struct {
	Crop    string  // The crop value (e.g., "3840:1632:0:264")
	Count   int     // Number of samples with this crop
	Percent float64 // Percentage of total samples
}

// CropSummary relays the crop detection result.
type CropSummary struct {
	Message      string
	Crop         string
	Required     bool
	Disabled     bool
	Candidates   []CropCandidate // All detected crop values (for debugging multiple ratios)
	TotalSamples int             // Total samples analyzed
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
	Progress func(ProgressUpdate)
}
