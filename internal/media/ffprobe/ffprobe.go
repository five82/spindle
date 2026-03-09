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
	Tags          map[string]string `json:"tags"`
	Disposition   map[string]int    `json:"disposition"`
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

// AudioStreamCount returns the number of streams with codec_type "audio".
func (r *Result) AudioStreamCount() int {
	n := 0
	for _, s := range r.Streams {
		if s.CodecType == "audio" {
			n++
		}
	}
	return n
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
