package encoding

import (
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/encodingstate"
	"spindle/internal/logging"
	"spindle/internal/services/drapto"
)

func progressMessageText(update drapto.ProgressUpdate) string {
	message := strings.TrimSpace(update.Message)
	if message != "" {
		return message
	}
	if update.Percent < 0 {
		return ""
	}
	label := formatStageLabel(update.Stage)
	base := fmt.Sprintf("%s %.1f%%", label, update.Percent)
	extras := make([]string, 0, 2)
	if update.ETA > 0 {
		if formatted := formatETA(update.ETA); formatted != "" {
			extras = append(extras, fmt.Sprintf("ETA %s", formatted))
		}
	}
	if update.Speed > 0 {
		extras = append(extras, fmt.Sprintf("@ %.1fx", update.Speed))
	}
	if len(extras) == 0 {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(extras, ", "))
}

func formatStageLabel(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return "Progress"
	}
	parts := strings.FieldsFunc(stage, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	if len(parts) == 0 {
		return capitalizeASCII(stage)
	}
	for i, part := range parts {
		parts[i] = capitalizeASCII(part)
	}
	return strings.Join(parts, " ")
}

func formatETA(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second
	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || hours > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || (hours == 0 && minutes == 0) {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, "")
}

func capitalizeASCII(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func loadEncodingSnapshot(logger *slog.Logger, raw string) encodingstate.Snapshot {
	snapshot, err := encodingstate.Unmarshal(raw)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to parse encoding snapshot; progress details may be reset",
				logging.Error(err),
				logging.String(logging.FieldEventType, "encoding_snapshot_parse_failed"),
				logging.String(logging.FieldErrorHint, "check encoding_details_json schema changes"),
			)
		}
		return encodingstate.Snapshot{}
	}
	return snapshot
}

func applyDraptoUpdate(snapshot *encodingstate.Snapshot, update drapto.ProgressUpdate, summary string) bool {
	if snapshot == nil {
		return false
	}
	changed := false
	switch update.Type {
	case drapto.EventTypeStageProgress, drapto.EventTypeEncodingProgress, drapto.EventTypeEncodingStarted, drapto.EventTypeUnknown:
		if mergeProgressSnapshot(snapshot, update, summary) {
			changed = true
		}
	}
	switch update.Type {
	case drapto.EventTypeHardware:
		if mergeHardwareSnapshot(snapshot, update.Hardware) {
			changed = true
		}
	case drapto.EventTypeInitialization:
		if mergeVideoSnapshot(snapshot, update.Video) {
			changed = true
		}
	case drapto.EventTypeCropResult:
		if mergeCropSnapshot(snapshot, update.Crop) {
			changed = true
		}
	case drapto.EventTypeEncodingConfig:
		if mergeConfigSnapshot(snapshot, update.EncodingConfig) {
			changed = true
		}
	case drapto.EventTypeValidation:
		if mergeValidationSnapshot(snapshot, update.Validation) {
			changed = true
		}
	case drapto.EventTypeEncodingComplete:
		if mergeResultSnapshot(snapshot, update.Result) {
			changed = true
		}
	case drapto.EventTypeWarning:
		if mergeWarningSnapshot(snapshot, update.Warning, update.Message) {
			changed = true
		}
	case drapto.EventTypeError:
		if mergeErrorSnapshot(snapshot, update.Error) {
			changed = true
		}
	}
	return changed
}

func mergeProgressSnapshot(snapshot *encodingstate.Snapshot, update drapto.ProgressUpdate, summary string) bool {
	changed := false
	if stage := strings.TrimSpace(update.Stage); stage != "" && stage != snapshot.Stage {
		snapshot.Stage = stage
		changed = true
	}
	if update.Percent >= 0 && update.Percent != snapshot.Percent {
		snapshot.Percent = update.Percent
		changed = true
	}
	if summary := strings.TrimSpace(summary); summary != "" && summary != snapshot.Message {
		snapshot.Message = summary
		changed = true
	}
	if update.ETA > 0 {
		eta := update.ETA.Seconds()
		if snapshot.ETASeconds != eta {
			snapshot.ETASeconds = eta
			changed = true
		}
	}
	if update.Speed > 0 && update.Speed != snapshot.Speed {
		snapshot.Speed = update.Speed
		changed = true
	}
	if update.FPS > 0 && update.FPS != snapshot.FPS {
		snapshot.FPS = update.FPS
		changed = true
	}
	if bitrate := strings.TrimSpace(update.Bitrate); bitrate != "" && bitrate != snapshot.Bitrate {
		snapshot.Bitrate = bitrate
		changed = true
	}
	if update.TotalFrames > 0 && update.TotalFrames != snapshot.TotalFrames {
		snapshot.TotalFrames = update.TotalFrames
		changed = true
	}
	if update.CurrentFrame > 0 && update.CurrentFrame != snapshot.CurrentFrame {
		snapshot.CurrentFrame = update.CurrentFrame
		changed = true
	}
	return changed
}

func mergeHardwareSnapshot(snapshot *encodingstate.Snapshot, info *drapto.HardwareInfo) bool {
	if info == nil {
		return false
	}
	host := strings.TrimSpace(info.Hostname)
	if host == "" {
		return false
	}
	if snapshot.Hardware == nil {
		snapshot.Hardware = &encodingstate.Hardware{}
	}
	if snapshot.Hardware.Hostname == host {
		return false
	}
	snapshot.Hardware.Hostname = host
	return true
}

func mergeVideoSnapshot(snapshot *encodingstate.Snapshot, info *drapto.VideoInfo) bool {
	if info == nil {
		return false
	}
	if snapshot.Video == nil {
		snapshot.Video = &encodingstate.Video{}
	}
	changed := false
	changed = setString(&snapshot.Video.InputFile, info.InputFile) || changed
	changed = setString(&snapshot.Video.OutputFile, info.OutputFile) || changed
	changed = setString(&snapshot.Video.Duration, info.Duration) || changed
	changed = setString(&snapshot.Video.Resolution, info.Resolution) || changed
	changed = setString(&snapshot.Video.Category, info.Category) || changed
	changed = setString(&snapshot.Video.DynamicRange, info.DynamicRange) || changed
	changed = setString(&snapshot.Video.AudioDescription, info.AudioDescription) || changed
	return changed
}

func mergeCropSnapshot(snapshot *encodingstate.Snapshot, summary *drapto.CropSummary) bool {
	if summary == nil {
		return false
	}
	if snapshot.Crop == nil {
		snapshot.Crop = &encodingstate.Crop{}
	}
	changed := false
	changed = setString(&snapshot.Crop.Message, summary.Message) || changed
	changed = setString(&snapshot.Crop.Crop, summary.Crop) || changed
	if snapshot.Crop.Required != summary.Required {
		snapshot.Crop.Required = summary.Required
		changed = true
	}
	if snapshot.Crop.Disabled != summary.Disabled {
		snapshot.Crop.Disabled = summary.Disabled
		changed = true
	}
	return changed
}

func mergeConfigSnapshot(snapshot *encodingstate.Snapshot, cfg *drapto.EncodingConfig) bool {
	if cfg == nil {
		return false
	}
	if snapshot.Config == nil {
		snapshot.Config = &encodingstate.Config{}
	}
	changed := false
	changed = setString(&snapshot.Config.Encoder, cfg.Encoder) || changed
	changed = setString(&snapshot.Config.Preset, cfg.Preset) || changed
	changed = setString(&snapshot.Config.Tune, cfg.Tune) || changed
	changed = setString(&snapshot.Config.Quality, cfg.Quality) || changed
	changed = setString(&snapshot.Config.PixelFormat, cfg.PixelFormat) || changed
	changed = setString(&snapshot.Config.MatrixCoefficients, cfg.MatrixCoefficients) || changed
	changed = setString(&snapshot.Config.AudioCodec, cfg.AudioCodec) || changed
	changed = setString(&snapshot.Config.AudioDescription, cfg.AudioDescription) || changed
	changed = setString(&snapshot.Config.DraptoPreset, cfg.DraptoPreset) || changed
	changed = setString(&snapshot.Config.SVTParams, cfg.SVTParams) || changed
	settings := make([]encodingstate.PresetSetting, 0, len(cfg.PresetSettings))
	for _, setting := range cfg.PresetSettings {
		settings = append(settings, encodingstate.PresetSetting{
			Key:   strings.TrimSpace(setting.Key),
			Value: strings.TrimSpace(setting.Value),
		})
	}
	if !presetSettingsEqual(snapshot.Config.PresetSettings, settings) {
		snapshot.Config.PresetSettings = settings
		changed = true
	}
	return changed
}

func mergeValidationSnapshot(snapshot *encodingstate.Snapshot, summary *drapto.ValidationSummary) bool {
	if summary == nil {
		return false
	}
	if snapshot.Validation == nil {
		snapshot.Validation = &encodingstate.Validation{}
	}
	changed := false
	if snapshot.Validation.Passed != summary.Passed {
		snapshot.Validation.Passed = summary.Passed
		changed = true
	}
	steps := make([]encodingstate.ValidationStep, 0, len(summary.Steps))
	for _, step := range summary.Steps {
		steps = append(steps, encodingstate.ValidationStep{
			Name:    strings.TrimSpace(step.Name),
			Passed:  step.Passed,
			Details: strings.TrimSpace(step.Details),
		})
	}
	if !validationStepsEqual(snapshot.Validation.Steps, steps) {
		snapshot.Validation.Steps = steps
		changed = true
	}
	return changed
}

func mergeResultSnapshot(snapshot *encodingstate.Snapshot, result *drapto.EncodingResult) bool {
	if result == nil {
		return false
	}
	if snapshot.Result == nil {
		snapshot.Result = &encodingstate.Result{}
	}
	changed := false
	changed = setString(&snapshot.Result.InputFile, result.InputFile) || changed
	changed = setString(&snapshot.Result.OutputFile, result.OutputFile) || changed
	changed = setString(&snapshot.Result.OutputPath, result.OutputPath) || changed
	changed = setString(&snapshot.Result.VideoStream, result.VideoStream) || changed
	changed = setString(&snapshot.Result.AudioStream, result.AudioStream) || changed
	if snapshot.Result.OriginalSize != result.OriginalSize {
		snapshot.Result.OriginalSize = result.OriginalSize
		changed = true
	}
	if snapshot.Result.EncodedSize != result.EncodedSize {
		snapshot.Result.EncodedSize = result.EncodedSize
		changed = true
	}
	if snapshot.Result.AverageSpeed != result.AverageSpeed {
		snapshot.Result.AverageSpeed = result.AverageSpeed
		changed = true
	}
	durationSeconds := result.Duration.Seconds()
	if snapshot.Result.DurationSeconds != durationSeconds {
		snapshot.Result.DurationSeconds = durationSeconds
		changed = true
	}
	if snapshot.Result.SizeReductionPercent != result.SizeReductionPercent {
		snapshot.Result.SizeReductionPercent = result.SizeReductionPercent
		changed = true
	}
	return changed
}

func mergeWarningSnapshot(snapshot *encodingstate.Snapshot, warning, fallback string) bool {
	message := strings.TrimSpace(warning)
	if message == "" {
		message = strings.TrimSpace(fallback)
	}
	if message == "" || snapshot.Warning == message {
		return false
	}
	snapshot.Warning = message
	return true
}

func mergeErrorSnapshot(snapshot *encodingstate.Snapshot, issue *drapto.ReporterIssue) bool {
	if issue == nil {
		return false
	}
	next := &encodingstate.Issue{
		Title:      strings.TrimSpace(issue.Title),
		Message:    strings.TrimSpace(issue.Message),
		Context:    strings.TrimSpace(issue.Context),
		Suggestion: strings.TrimSpace(issue.Suggestion),
	}
	if issuesEqual(snapshot.Error, next) {
		return false
	}
	snapshot.Error = next
	return true
}

func setString(target *string, value string) bool {
	trimmed := strings.TrimSpace(value)
	if *target == trimmed {
		return false
	}
	*target = trimmed
	return true
}

func presetSettingsEqual(a, b []encodingstate.PresetSetting) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func validationStepsEqual(a, b []encodingstate.ValidationStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Passed != b[i].Passed || a[i].Details != b[i].Details {
			return false
		}
	}
	return true
}

func issuesEqual(a *encodingstate.Issue, b *encodingstate.Issue) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Title == b.Title && a.Message == b.Message && a.Context == b.Context && a.Suggestion == b.Suggestion
}
