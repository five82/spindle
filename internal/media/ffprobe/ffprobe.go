// Package ffprobe wraps the ffprobe CLI to extract stream and format metadata
// from media files as structured JSON.
package ffprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// FlexString unmarshals from both JSON strings and numbers, storing the result
// as a string. ffprobe is inconsistent: mastering display fields (max_luminance)
// are strings like "1000/1", but content light level fields (max_content,
// max_average) are plain integers.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	// Fall back to number.
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexString(n.String())
		return nil
	}
	return fmt.Errorf("FlexString: cannot unmarshal %s", string(b))
}

// Stream represents a single elementary stream (video, audio, subtitle, etc.)
// as reported by ffprobe.
type Stream struct {
	Index         int               `json:"index"`
	CodecName     string            `json:"codec_name"`
	CodecType     string            `json:"codec_type"`
	CodecTag      string            `json:"codec_tag_string"`
	CodecLong     string            `json:"codec_long_name"`
	Duration      string            `json:"duration"`
	BitRate       string            `json:"bit_rate"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	SampleRate    string            `json:"sample_rate"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	Profile       string            `json:"profile"`
	PixFmt        string            `json:"pix_fmt"`
	ColorRange    string            `json:"color_range"`
	ColorSpace    string            `json:"color_space"`
	ColorTransfer string            `json:"color_transfer"`
	ColorPrimaries string           `json:"color_primaries"`
	SideDataList  []SideData        `json:"side_data_list"`
	Tags          map[string]string `json:"tags"`
	Disposition   map[string]int    `json:"disposition"`
}

// SideData represents a side data entry from ffprobe (e.g. mastering display metadata).
type SideData struct {
	Type string `json:"side_data_type"`
	// Mastering display metadata fields
	RedX          string `json:"red_x,omitempty"`
	RedY          string `json:"red_y,omitempty"`
	GreenX        string `json:"green_x,omitempty"`
	GreenY        string `json:"green_y,omitempty"`
	BlueX         string `json:"blue_x,omitempty"`
	BlueY         string `json:"blue_y,omitempty"`
	WhitePointX   string `json:"white_point_x,omitempty"`
	WhitePointY   string `json:"white_point_y,omitempty"`
	MinLuminance  string `json:"min_luminance,omitempty"`
	MaxLuminance  string `json:"max_luminance,omitempty"`
	// Content light level fields — ffprobe emits these as integers, not strings.
	MaxContent FlexString `json:"max_content,omitempty"`
	MaxAverage FlexString `json:"max_average,omitempty"`
}

// Format holds container-level metadata as reported by ffprobe.
type Format struct {
	Filename   string `json:"filename"`
	NBStreams  int    `json:"nb_streams"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
	FormatName string `json:"format_name"`
}

// Result holds the parsed ffprobe output for a single media file.
type Result struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
	rawJSON []byte
}

// RawJSON returns the raw JSON response from ffprobe.
func (r *Result) RawJSON() []byte {
	return r.rawJSON
}

// VideoStreamCount returns the number of streams with codec_type "video".
func (r *Result) VideoStreamCount() int {
	n := 0
	for _, s := range r.Streams {
		if s.CodecType == "video" {
			n++
		}
	}
	return n
}

// AudioStreams returns only the audio streams from the probe result.
func (r *Result) AudioStreams() []Stream {
	var out []Stream
	for _, s := range r.Streams {
		if s.CodecType == "audio" {
			out = append(out, s)
		}
	}
	return out
}

// AudioStreamCount returns the number of streams with codec_type "audio".
func (r *Result) AudioStreamCount() int {
	return len(r.AudioStreams())
}

// DurationSeconds parses Format.Duration to a float64. Returns 0 on error.
func (r *Result) DurationSeconds() float64 {
	v, err := strconv.ParseFloat(r.Format.Duration, 64)
	if err != nil {
		return 0
	}
	return v
}

// SizeBytes parses Format.Size to an int64. Returns 0 on error.
func (r *Result) SizeBytes() int64 {
	v, err := strconv.ParseInt(r.Format.Size, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// BitRate parses Format.BitRate to an int64. Returns 0 on error.
func (r *Result) BitRate() int64 {
	v, err := strconv.ParseInt(r.Format.BitRate, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// Inspect runs ffprobe against the file at path and returns the parsed result.
// If binary is empty it defaults to "ffprobe".
func Inspect(ctx context.Context, binary, path string) (*Result, error) {
	if binary == "" {
		binary = "ffprobe"
	}

	cmd := exec.CommandContext(ctx, binary,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}

	var result Result
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	result.rawJSON = out

	return &result, nil
}
