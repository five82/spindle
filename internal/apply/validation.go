package apply

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/media/ffprobe"
)

const audioDurationToleranceSeconds = 5.0

// validateAudioTargetDurations probes each distinct path and compares audio
// stream durations against the video. It returns the video duration measured
// per path (partially filled when validation stops on the first mismatch) so
// the final verification can detect a mux that changed the runtime.
func validateAudioTargetDurations(ctx context.Context, paths []string) (map[string]float64, error) {
	durations := make(map[string]float64, len(paths))
	for _, path := range paths {
		if _, seen := durations[path]; seen {
			continue
		}

		result, err := ffprobe.Inspect(ctx, "", path)
		if err != nil {
			return durations, fmt.Errorf("ffprobe %s: %w", path, err)
		}
		durations[path] = videoDurationSeconds(result)
		if err := validateAudioDurations(filepath.Base(path), result); err != nil {
			return durations, err
		}
	}
	return durations, nil
}

// videoDurationSeconds resolves a file's video runtime, preferring the
// container duration and falling back to the first video stream's.
func videoDurationSeconds(result *ffprobe.Result) float64 {
	if duration := result.DurationSeconds(); duration > 0 {
		return duration
	}
	return firstStreamDuration(result, "video")
}

func validateAudioDurations(label string, result *ffprobe.Result) error {
	videoDuration := videoDurationSeconds(result)
	if videoDuration <= 0 {
		return nil
	}

	for i, stream := range result.AudioStreams() {
		audioDuration, ok := streamDurationSeconds(stream)
		if !ok {
			continue
		}
		diff := math.Abs(videoDuration - audioDuration)
		if diff <= audioDurationToleranceSeconds {
			continue
		}
		// Commentary disposition is set before validation. Commentary often
		// ends before the source disc's credits, so a shorter duration
		// than video is a source characteristic, not pipeline truncation.
		// Under half the video still reads as truncation.
		if stream.Disposition["comment"] == 1 && audioDuration < videoDuration && audioDuration >= videoDuration/2 {
			continue
		}
		return fmt.Errorf("%s: audio stream %d duration %.3fs differs from video %.3fs by %.3fs", label, i, audioDuration, videoDuration, diff)
	}
	return nil
}

func firstStreamDuration(result *ffprobe.Result, codecType string) float64 {
	for _, stream := range result.Streams {
		if stream.CodecType != codecType {
			continue
		}
		if duration, ok := streamDurationSeconds(stream); ok {
			return duration
		}
	}
	return 0
}

func streamDurationSeconds(stream ffprobe.Stream) (float64, bool) {
	if duration, ok := parseDurationSeconds(stream.Duration); ok {
		return duration, true
	}
	if stream.Tags == nil {
		return 0, false
	}
	if duration, ok := parseDurationSeconds(stream.Tags["DURATION"]); ok {
		return duration, true
	}
	return parseDurationSeconds(stream.Tags["duration"])
}

func parseDurationSeconds(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "N/A") {
		return 0, false
	}

	if !strings.Contains(raw, ":") {
		seconds, err := strconv.ParseFloat(raw, 64)
		return seconds, err == nil && seconds > 0
	}

	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, false
	}
	return float64(hours*3600+minutes*60) + seconds, true
}
