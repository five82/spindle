// Package reel provides a Go library for AV1 video encoding with SVT-AV1.
package reel

import "time"

// Event types for Spindle integration.
const (
	EventTypeHardware           = "hardware"
	EventTypeInitialization     = "initialization"
	EventTypeStageProgress      = "stage_progress"
	EventTypeEncodingStarted    = "encoding_started"
	EventTypeEncodingConfig     = "encoding_config"
	EventTypeCropResult         = "crop_result"
	EventTypeEncodingProgress   = "encoding_progress"
	EventTypeValidationComplete = "validation_complete"
	EventTypeEncodingComplete   = "encoding_complete"
	EventTypeOperationComplete  = "operation_complete"
	EventTypeBatchStarted       = "batch_started"
	EventTypeFileProgress       = "file_progress"
	EventTypeBatchComplete      = "batch_complete"
	EventTypeWarning            = "warning"
	EventTypeError              = "error"
)

// Event is the interface for all reel events.
type Event interface {
	Type() string
	Timestamp() int64
}

// BaseEvent contains common fields for all events.
type BaseEvent struct {
	EventType string `json:"type"`
	Time      int64  `json:"timestamp"`
}

func (e BaseEvent) Type() string     { return e.EventType }
func (e BaseEvent) Timestamp() int64 { return e.Time }

// EncodingProgressEvent represents encoding progress updates.
type EncodingProgressEvent struct {
	BaseEvent
	Percent     float32 `json:"percent"`
	Speed       float32 `json:"speed"`
	RecentSpeed float32 `json:"recent_speed"`
	FPS         float32 `json:"fps"`
	ETASeconds  int64   `json:"eta_seconds"`
}

// ValidationCompleteEvent represents validation completion.
type ValidationCompleteEvent struct {
	BaseEvent
	ValidationPassed bool             `json:"validation_passed"`
	ValidationSteps  []ValidationStep `json:"validation_steps"`
}

// ValidationStep represents a single validation check.
type ValidationStep struct {
	Step    string `json:"step"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// EncodingCompleteEvent represents successful encode completion.
type EncodingCompleteEvent struct {
	BaseEvent
	OutputFile                string  `json:"output_file"`
	OriginalSize              uint64  `json:"original_size"`
	EncodedSize               uint64  `json:"encoded_size"`
	SizeReductionPercent      float64 `json:"size_reduction_percent"`
	VideoOriginalSize         uint64  `json:"video_original_size,omitempty"`
	VideoEncodedSize          uint64  `json:"video_encoded_size,omitempty"`
	VideoSizeReductionPercent float64 `json:"video_size_reduction_percent,omitempty"`
}

// WarningEvent represents a warning message.
type WarningEvent struct {
	BaseEvent
	Message string `json:"message"`
}

// ErrorEvent represents an error.
type ErrorEvent struct {
	BaseEvent
	Title      string `json:"title"`
	Message    string `json:"message"`
	Context    string `json:"context"`
	Suggestion string `json:"suggestion"`
}

// BatchCompleteEvent represents batch completion.
type BatchCompleteEvent struct {
	BaseEvent
	SuccessfulCount           int     `json:"successful_count"`
	TotalFiles                int     `json:"total_files"`
	TotalSizeReductionPercent float64 `json:"total_size_reduction_percent"`
}

// EventHandler is called with events during encoding.
type EventHandler func(Event) error

// NewTimestamp returns the current Unix timestamp.
func NewTimestamp() int64 {
	return time.Now().Unix()
}
