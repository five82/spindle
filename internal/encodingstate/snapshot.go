package encodingstate

import (
	"encoding/json"
	"strings"
)

// Issue describes a problem encountered during encoding.
type Issue struct {
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	Context    string `json:"context,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ValidationStep records the result of a single validation check.
type ValidationStep struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

// Validation holds the aggregate result of all validation steps.
type Validation struct {
	Passed bool             `json:"passed"`
	Steps  []ValidationStep `json:"steps,omitempty"`
}

// Snapshot captures the full state of an encoding operation at a point in time.
type Snapshot struct {
	Percent               float64     `json:"percent,omitempty"`
	ETASeconds            float64     `json:"eta_seconds,omitempty"`
	FPS                   float64     `json:"fps,omitempty"`
	CurrentFrame          int64       `json:"current_frame,omitempty"`
	TotalFrames           int64       `json:"total_frames,omitempty"`
	CurrentOutputBytes    int64       `json:"current_output_bytes,omitempty"`
	EstimatedTotalBytes   int64       `json:"estimated_total_bytes,omitempty"`
	Substage              string      `json:"substage,omitempty"`
	InputFile             string      `json:"input_file,omitempty"`
	Resolution            string      `json:"resolution,omitempty"`
	DynamicRange          string      `json:"dynamic_range,omitempty"`
	Encoder               string      `json:"encoder,omitempty"`
	Preset                string      `json:"preset,omitempty"`
	Quality               string      `json:"quality,omitempty"`
	Tune                  string      `json:"tune,omitempty"`
	AudioCodec            string      `json:"audio_codec,omitempty"`
	DraptoPreset          string      `json:"drapto_preset,omitempty"`
	CropFilter            string      `json:"crop_filter,omitempty"`
	CropRequired          bool        `json:"crop_required,omitempty"`
	CropMessage           string      `json:"crop_message,omitempty"`
	OriginalSize          int64       `json:"original_size,omitempty"`
	EncodedSize           int64       `json:"encoded_size,omitempty"`
	SizeReductionPercent  float64     `json:"size_reduction_percent,omitempty"`
	AverageSpeed          float64     `json:"average_speed,omitempty"`
	EncodeDurationSeconds float64     `json:"encode_duration_seconds,omitempty"`
	Warning               string      `json:"warning,omitempty"`
	Error                 *Issue      `json:"error,omitempty"`
	Validation            *Validation `json:"validation,omitempty"`
}

// IsZero returns true when all fields are zero, empty, or nil.
func (s Snapshot) IsZero() bool {
	return s == Snapshot{}
}

// Reset zeroes all fields of the snapshot.
func (s *Snapshot) Reset() {
	*s = Snapshot{}
}

// Marshal returns the JSON representation of the snapshot.
// Returns an empty string for a zero-value snapshot.
func (s Snapshot) Marshal() string {
	if s.IsZero() {
		return ""
	}
	data, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(data)
}

// Unmarshal parses a JSON string into a Snapshot.
// Empty or whitespace-only input returns a zero Snapshot with no error.
func Unmarshal(raw string) (Snapshot, error) {
	if strings.TrimSpace(raw) == "" {
		return Snapshot{}, nil
	}
	var s Snapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}
