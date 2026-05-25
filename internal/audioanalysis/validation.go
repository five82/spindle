package audioanalysis

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

func validateAudioTargetDurations(ctx context.Context, paths []string) error {
	seen := make(map[string]bool)
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true

		result, err := ffprobe.Inspect(ctx, "", path)
		if err != nil {
			return fmt.Errorf("ffprobe %s: %w", path, err)
		}
		if err := validateAudioDurations(filepath.Base(path), result); err != nil {
			return err
		}
	}
	return nil
}

func validateAudioDurations(label string, result *ffprobe.Result) error {
	videoDuration := result.DurationSeconds()
	if videoDuration <= 0 {
		videoDuration = firstStreamDuration(result, "video")
	}
	if videoDuration <= 0 {
		return nil
	}

	for i, stream := range result.AudioStreams() {
		audioDuration, ok := streamDurationSeconds(stream)
		if !ok {
			continue
		}
		if diff := math.Abs(videoDuration - audioDuration); diff > audioDurationToleranceSeconds {
			return fmt.Errorf("%s: audio stream %d duration %.3fs differs from video %.3fs by %.3fs", label, i, audioDuration, videoDuration, diff)
		}
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
