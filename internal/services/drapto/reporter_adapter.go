package drapto

import (
	"time"

	draptolib "github.com/five82/drapto"
)

// spindleReporter adapts the Drapto Reporter interface to Spindle's
// ProgressUpdate callback system.
type spindleReporter struct {
	callback func(ProgressUpdate)
}

func newSpindleReporter(callback func(ProgressUpdate)) *spindleReporter {
	return &spindleReporter{callback: callback}
}

func (r *spindleReporter) Hardware(s draptolib.HardwareSummary) {
	r.callback(ProgressUpdate{
		Type:      EventTypeHardware,
		Timestamp: time.Now(),
		Hardware:  &HardwareInfo{Hostname: s.Hostname},
	})
}

func (r *spindleReporter) Initialization(s draptolib.InitializationSummary) {
	r.callback(ProgressUpdate{
		Type:      EventTypeInitialization,
		Timestamp: time.Now(),
		Video: &VideoInfo{
			InputFile:        s.InputFile,
			OutputFile:       s.OutputFile,
			Duration:         s.Duration,
			Resolution:       s.Resolution,
			Category:         s.Category,
			DynamicRange:     s.DynamicRange,
			AudioDescription: s.AudioDescription,
		},
	})
}

func (r *spindleReporter) StageProgress(s draptolib.StageProgress) {
	var eta time.Duration
	if s.ETA != nil {
		eta = *s.ETA
	}
	r.callback(ProgressUpdate{
		Type:      EventTypeStageProgress,
		Timestamp: time.Now(),
		Percent:   float64(s.Percent),
		Stage:     s.Stage,
		Message:   s.Message,
		ETA:       eta,
	})
}

func (r *spindleReporter) CropResult(s draptolib.CropSummary) {
	// Convert crop candidates
	var candidates []CropCandidate
	for _, c := range s.Candidates {
		candidates = append(candidates, CropCandidate{
			Crop:    c.Crop,
			Count:   c.Count,
			Percent: c.Percent,
		})
	}

	r.callback(ProgressUpdate{
		Type:      EventTypeCropResult,
		Timestamp: time.Now(),
		Crop: &CropSummary{
			Message:      s.Message,
			Crop:         s.Crop,
			Required:     s.Required,
			Disabled:     s.Disabled,
			Candidates:   candidates,
			TotalSamples: s.TotalSamples,
		},
	})
}

func (r *spindleReporter) EncodingConfig(s draptolib.EncodingConfigSummary) {
	// Convert preset settings from [][2]string to []PresetSetting
	settings := make([]PresetSetting, 0, len(s.DraptoPresetSettings))
	for _, pair := range s.DraptoPresetSettings {
		settings = append(settings, PresetSetting{Key: pair[0], Value: pair[1]})
	}
	r.callback(ProgressUpdate{
		Type:      EventTypeEncodingConfig,
		Timestamp: time.Now(),
		EncodingConfig: &EncodingConfig{
			Encoder:            s.Encoder,
			Preset:             s.Preset,
			Tune:               s.Tune,
			Quality:            s.Quality,
			PixelFormat:        s.PixelFormat,
			MatrixCoefficients: s.MatrixCoefficients,
			AudioCodec:         s.AudioCodec,
			AudioDescription:   s.AudioDescription,
			DraptoPreset:       s.DraptoPreset,
			PresetSettings:     settings,
			SVTParams:          s.SVTAV1Params,
		},
	})
}

func (r *spindleReporter) EncodingStarted(totalFrames uint64) {
	r.callback(ProgressUpdate{
		Type:        EventTypeEncodingStarted,
		Timestamp:   time.Now(),
		TotalFrames: int64(totalFrames),
	})
}

func (r *spindleReporter) EncodingProgress(s draptolib.ProgressSnapshot) {
	r.callback(ProgressUpdate{
		Type:         EventTypeEncodingProgress,
		Timestamp:    time.Now(),
		Percent:      float64(s.Percent),
		Stage:        "encoding",
		Speed:        float64(s.Speed),
		FPS:          float64(s.FPS),
		ETA:          s.ETA,
		Bitrate:      s.Bitrate,
		TotalFrames:  int64(s.TotalFrames),
		CurrentFrame: int64(s.CurrentFrame),
	})
}

func (r *spindleReporter) ValidationComplete(s draptolib.ValidationSummary) {
	steps := make([]ValidationStep, 0, len(s.Steps))
	for _, step := range s.Steps {
		steps = append(steps, ValidationStep{
			Name:    step.Name,
			Passed:  step.Passed,
			Details: step.Details,
		})
	}
	r.callback(ProgressUpdate{
		Type:      EventTypeValidation,
		Timestamp: time.Now(),
		Validation: &ValidationSummary{
			Passed: s.Passed,
			Steps:  steps,
		},
	})
}

func (r *spindleReporter) EncodingComplete(s draptolib.EncodingOutcome) {
	r.callback(ProgressUpdate{
		Type:      EventTypeEncodingComplete,
		Timestamp: time.Now(),
		Result: &EncodingResult{
			InputFile:    s.InputFile,
			OutputFile:   s.OutputFile,
			OriginalSize: int64(s.OriginalSize),
			EncodedSize:  int64(s.EncodedSize),
			VideoStream:  s.VideoStream,
			AudioStream:  s.AudioStream,
			AverageSpeed: float64(s.AverageSpeed),
			OutputPath:   s.OutputPath,
			Duration:     s.TotalTime,
		},
	})
}

func (r *spindleReporter) Warning(message string) {
	r.callback(ProgressUpdate{
		Type:      EventTypeWarning,
		Timestamp: time.Now(),
		Warning:   message,
	})
}

func (r *spindleReporter) Error(e draptolib.ReporterError) {
	r.callback(ProgressUpdate{
		Type:      EventTypeError,
		Timestamp: time.Now(),
		Error: &ReporterIssue{
			Title:      e.Title,
			Message:    e.Message,
			Context:    e.Context,
			Suggestion: e.Suggestion,
		},
	})
}

func (r *spindleReporter) OperationComplete(message string) {
	r.callback(ProgressUpdate{
		Type:              EventTypeOperationComplete,
		Timestamp:         time.Now(),
		OperationComplete: message,
	})
}

func (r *spindleReporter) BatchStarted(s draptolib.BatchStartInfo) {
	r.callback(ProgressUpdate{
		Type:      EventTypeBatchStarted,
		Timestamp: time.Now(),
		BatchStart: &BatchStartInfo{
			TotalFiles: s.TotalFiles,
			FileList:   append([]string(nil), s.FileList...),
			OutputDir:  s.OutputDir,
		},
	})
}

func (r *spindleReporter) FileProgress(s draptolib.FileProgressContext) {
	r.callback(ProgressUpdate{
		Type:      EventTypeFileProgress,
		Timestamp: time.Now(),
		FileProgress: &FileProgress{
			CurrentFile: s.CurrentFile,
			TotalFiles:  s.TotalFiles,
		},
	})
}

func (r *spindleReporter) BatchComplete(s draptolib.BatchSummary) {
	r.callback(ProgressUpdate{
		Type:      EventTypeBatchComplete,
		Timestamp: time.Now(),
		BatchSummary: &BatchSummary{
			SuccessfulCount:   s.SuccessfulCount,
			TotalFiles:        s.TotalFiles,
			TotalOriginalSize: int64(s.TotalOriginalSize),
			TotalEncodedSize:  int64(s.TotalEncodedSize),
			TotalDuration:     s.TotalDuration,
		},
	})
}

var _ draptolib.Reporter = (*spindleReporter)(nil)
