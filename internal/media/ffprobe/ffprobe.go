package ffprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// Result represents the parsed output from an ffprobe inspection.
type Result struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
	raw     []byte
}

// Stream describes a single stream in the media container.
type Stream struct {
	Index      int    `json:"index"`
	CodecName  string `json:"codec_name"`
	CodecType  string `json:"codec_type"`
	CodecTag   string `json:"codec_tag_string"`
	Duration   string `json:"duration"`
	BitRate    string `json:"bit_rate"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

// Format captures container-level metadata extracted by ffprobe.
type Format struct {
	Filename   string `json:"filename"`
	NBStreams  int    `json:"nb_streams"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
	FormatName string `json:"format_name"`
}

// Inspect executes ffprobe against the provided path and decodes the JSON response.
func Inspect(ctx context.Context, binary string, path string) (Result, error) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = "ffprobe"
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return Result{}, errors.New("ffprobe inspect: empty path")
	}

	cmd := exec.CommandContext(ctx, binary, "-v", "error", "-hide_banner", "-show_format", "-show_streams", "-of", "json", "--", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("ffprobe inspect: %w: %s", err, strings.TrimSpace(string(output)))
	}

	var result Result
	if err := json.Unmarshal(output, &result); err != nil {
		return Result{}, fmt.Errorf("ffprobe parse: %w", err)
	}
	result.raw = append([]byte(nil), output...)
	return result, nil
}

// RawJSON returns the raw ffprobe JSON payload.
func (r Result) RawJSON() []byte {
	return append([]byte(nil), r.raw...)
}

// VideoStreamCount returns the number of video streams discovered.
func (r Result) VideoStreamCount() int {
	count := 0
	for _, stream := range r.Streams {
		if strings.EqualFold(stream.CodecType, "video") {
			count++
		}
	}
	return count
}

// AudioStreamCount returns the number of audio streams discovered.
func (r Result) AudioStreamCount() int {
	count := 0
	for _, stream := range r.Streams {
		if strings.EqualFold(stream.CodecType, "audio") {
			count++
		}
	}
	return count
}

// DurationSeconds returns the container duration in seconds, or 0 when unavailable.
func (r Result) DurationSeconds() float64 {
	return parseFloat(r.Format.Duration)
}

// SizeBytes returns the reported container size in bytes, or 0 when unavailable.
func (r Result) SizeBytes() int64 {
	size := parseFloat(r.Format.Size)
	if math.IsNaN(size) || size < 0 {
		return 0
	}
	return int64(size)
}

// BitRate returns the container bitrate in bits per second, or 0 when unavailable.
func (r Result) BitRate() int64 {
	rate := parseFloat(r.Format.BitRate)
	if math.IsNaN(rate) || rate < 0 {
		return 0
	}
	return int64(rate)
}

func parseFloat(value string) float64 {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return 0
	}
	if parsed, err := strconv.ParseFloat(cleaned, 64); err == nil {
		return parsed
	}
	return math.NaN()
}
