package encodingstate

import (
	"encoding/json"
	"strings"
)

// Snapshot captures Drapto encoding telemetry in a transport-friendly form.
type Snapshot struct {
	JobLabel            string      `json:"jobLabel,omitempty"`
	EpisodeKey          string      `json:"episodeKey,omitempty"`
	EpisodeIndex        int         `json:"episodeIndex,omitempty"`
	EpisodeCount        int         `json:"episodeCount,omitempty"`
	Stage               string      `json:"stage,omitempty"`
	Message             string      `json:"message,omitempty"`
	Percent             float64     `json:"percent,omitempty"`
	ETASeconds          float64     `json:"etaSeconds,omitempty"`
	Speed               float64     `json:"speed,omitempty"`
	FPS                 float64     `json:"fps,omitempty"`
	Bitrate             string      `json:"bitrate,omitempty"`
	TotalFrames         int64       `json:"totalFrames,omitempty"`
	CurrentFrame        int64       `json:"currentFrame,omitempty"`
	CurrentOutputBytes  int64       `json:"currentOutputBytes,omitempty"`
	EstimatedTotalBytes int64       `json:"estimatedTotalBytes,omitempty"`
	Hardware            *Hardware   `json:"hardware,omitempty"`
	Video               *Video      `json:"video,omitempty"`
	Crop                *Crop       `json:"crop,omitempty"`
	Config              *Config     `json:"config,omitempty"`
	Validation          *Validation `json:"validation,omitempty"`
	Warning             string      `json:"warning,omitempty"`
	Error               *Issue      `json:"error,omitempty"`
	Result              *Result     `json:"result,omitempty"`
}

// Hardware describes the encoder host.
type Hardware struct {
	Hostname string `json:"hostname,omitempty"`
}

// Video summarizes the source media.
type Video struct {
	InputFile        string `json:"inputFile,omitempty"`
	OutputFile       string `json:"outputFile,omitempty"`
	Duration         string `json:"duration,omitempty"`
	Resolution       string `json:"resolution,omitempty"`
	Category         string `json:"category,omitempty"`
	DynamicRange     string `json:"dynamicRange,omitempty"`
	AudioDescription string `json:"audioDescription,omitempty"`
}

// Crop describes crop detection results.
type Crop struct {
	Message  string `json:"message,omitempty"`
	Crop     string `json:"crop,omitempty"`
	Required bool   `json:"required,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Config captures encoder configuration metadata.
type Config struct {
	Encoder            string          `json:"encoder,omitempty"`
	Preset             string          `json:"preset,omitempty"`
	Tune               string          `json:"tune,omitempty"`
	Quality            string          `json:"quality,omitempty"`
	PixelFormat        string          `json:"pixelFormat,omitempty"`
	MatrixCoefficients string          `json:"matrixCoefficients,omitempty"`
	AudioCodec         string          `json:"audioCodec,omitempty"`
	AudioDescription   string          `json:"audioDescription,omitempty"`
	DraptoPreset       string          `json:"draptoPreset,omitempty"`
	PresetSettings     []PresetSetting `json:"presetSettings,omitempty"`
	SVTParams          string          `json:"svtParams,omitempty"`
}

// PresetSetting is a key/value pair inside Drapto presets.
type PresetSetting struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

// Validation summarizes Drapto validation output.
type Validation struct {
	Passed bool             `json:"passed,omitempty"`
	Steps  []ValidationStep `json:"steps,omitempty"`
}

// ValidationStep captures a single validation check.
type ValidationStep struct {
	Name    string `json:"name,omitempty"`
	Passed  bool   `json:"passed,omitempty"`
	Details string `json:"details,omitempty"`
}

// Issue captures Drapto warning/error metadata.
type Issue struct {
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	Context    string `json:"context,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Result summarizes the encoding_complete payload.
type Result struct {
	InputFile            string  `json:"inputFile,omitempty"`
	OutputFile           string  `json:"outputFile,omitempty"`
	OutputPath           string  `json:"outputPath,omitempty"`
	OriginalSize         int64   `json:"originalSize,omitempty"`
	EncodedSize          int64   `json:"encodedSize,omitempty"`
	VideoStream          string  `json:"videoStream,omitempty"`
	AudioStream          string  `json:"audioStream,omitempty"`
	AverageSpeed         float64 `json:"averageSpeed,omitempty"`
	DurationSeconds      float64 `json:"durationSeconds,omitempty"`
	SizeReductionPercent float64 `json:"sizeReductionPercent,omitempty"`
}

// IsZero reports whether the snapshot has no meaningful data.
func (s Snapshot) IsZero() bool {
	return strings.TrimSpace(s.JobLabel) == "" &&
		strings.TrimSpace(s.EpisodeKey) == "" &&
		s.EpisodeIndex == 0 &&
		s.EpisodeCount == 0 &&
		strings.TrimSpace(s.Stage) == "" &&
		strings.TrimSpace(s.Message) == "" &&
		s.Percent == 0 &&
		s.ETASeconds == 0 &&
		s.Speed == 0 &&
		s.FPS == 0 &&
		strings.TrimSpace(s.Bitrate) == "" &&
		s.TotalFrames == 0 &&
		s.CurrentFrame == 0 &&
		s.CurrentOutputBytes == 0 &&
		s.EstimatedTotalBytes == 0 &&
		s.Hardware == nil &&
		s.Video == nil &&
		s.Crop == nil &&
		s.Config == nil &&
		s.Validation == nil &&
		strings.TrimSpace(s.Warning) == "" &&
		s.Error == nil &&
		s.Result == nil
}

// Marshal converts the snapshot into its JSON string form.
func (s Snapshot) Marshal() (string, error) {
	if s.IsZero() {
		return "", nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Unmarshal parses a snapshot from a JSON string. Empty input yields an empty snapshot.
func Unmarshal(raw string) (Snapshot, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Snapshot{}, nil
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}
