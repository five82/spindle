// Package reporter provides progress reporting interfaces and implementations.
package reporter

import "time"

// HardwareSummary contains hardware information.
type HardwareSummary struct {
	Hostname string
}

// InitializationSummary describes the current file before encoding.
type InitializationSummary struct {
	InputFile        string
	OutputFile       string
	Duration         string
	Resolution       string
	DynamicRange     string
	AudioDescription string
}

// CropSummary contains crop detection results.
type CropSummary struct {
	Message  string
	Crop     string
	Required bool
	Disabled bool
}

// EncodingConfigSummary contains encoding configuration.
type EncodingConfigSummary struct {
	Encoder            string
	EncoderVersion     string
	Preset             string
	Tune               string
	Quality            string
	PixelFormat        string
	MatrixCoefficients string
	AudioCodec         string
	AudioDescription   string
	SVTAV1Params       string
}

// ProgressSnapshot contains encoding progress information.
type ProgressSnapshot struct {
	CurrentFrame   uint64
	TotalFrames    uint64
	Percent        float32
	Speed          float32
	RecentSpeed    float32
	FPS            float32
	ETA            time.Duration
	Bitrate        string
	ChunksComplete int
	ChunksTotal    int
	ActiveWorkers  int
	TargetWorkers  int
	MaxWorkers     int
}

// ValidationSummary contains validation results.
type ValidationSummary struct {
	Passed bool
	Steps  []ValidationStep
}

// ValidationStep represents a single validation check.
type ValidationStep struct {
	Name    string
	Passed  bool
	Details string
}

// EncodingOutcome contains final encoding results.
type EncodingOutcome struct {
	InputFile         string
	OutputFile        string
	OriginalSize      uint64
	EncodedSize       uint64
	VideoOriginalSize uint64
	VideoEncodedSize  uint64
	VideoStream       string
	AudioStream       string
	TotalTime         time.Duration
	AverageSpeed      float32
	OutputPath        string
}

// ReporterError contains error information.
type ReporterError struct {
	Title      string
	Message    string
	Context    string
	Suggestion string
}

// BatchStartInfo contains batch start metadata.
type BatchStartInfo struct {
	TotalFiles int
	FileList   []string
	OutputDir  string
}

// FileProgressContext contains current file index within a batch.
type FileProgressContext struct {
	CurrentFile int
	TotalFiles  int
}

// BatchSummary contains batch completion information.
type BatchSummary struct {
	SuccessfulCount       int
	TotalFiles            int
	TotalOriginalSize     uint64
	TotalEncodedSize      uint64
	TotalDuration         time.Duration
	AverageSpeed          float32
	FileResults           []FileResult
	ValidationPassedCount int
	ValidationFailedCount int
}

// FileResult contains per-file encoding result.
type FileResult struct {
	Filename  string
	Reduction float64
}

// StageProgress represents a generic stage update.
type StageProgress struct {
	Stage   string
	Percent float32
	Message string
	ETA     *time.Duration
}
