package ripping

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/queue"
	"spindle/internal/services"
)

const (
	minRipFileSizeBytes = 10 * 1024 * 1024
)

var probeVideo = ffprobe.Inspect

func (r *Ripper) validateRippedArtifact(ctx context.Context, item *queue.Item, path string, startedAt time.Time) error {
	logger := logging.WithContext(ctx, r.logger)
	clean := strings.TrimSpace(path)
	if clean == "" {
		logger.Error("ripping validation failed", logging.String("reason", "empty path"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			"Ripping produced an empty file path",
			nil,
		)
	}
	info, err := os.Stat(clean)
	if err != nil {
		logger.Error("ripping validation failed", logging.String("reason", "stat failure"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			"Failed to stat ripped file",
			err,
		)
	}
	if info.IsDir() {
		logger.Error("ripping validation failed", logging.String("reason", "path is directory"), logging.String("ripped_file", clean))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			"Ripped artifact points to a directory",
			nil,
		)
	}
	if info.Size() < minRipFileSizeBytes {
		logger.Error(
			"ripping validation failed",
			logging.String("reason", "file too small"),
			logging.Int64("size_bytes", info.Size()),
		)
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			fmt.Sprintf("Ripped file %q is unexpectedly small (%d bytes)", clean, info.Size()),
			nil,
		)
	}

	binary := "ffprobe"
	if r.cfg != nil {
		binary = deps.ResolveFFprobePath(r.cfg.FFprobeBinary())
	}
	probe, err := probeVideo(ctx, binary, clean)
	if err != nil {
		logger.Error("ripping validation failed", logging.String("reason", "ffprobe"), logging.Error(err))
		return services.Wrap(
			services.ErrExternalTool,
			"ripping",
			"ffprobe validation",
			"Failed to inspect ripped file with ffprobe",
			err,
		)
	}
	if probe.VideoStreamCount() == 0 {
		logger.Error("ripping validation failed", logging.String("reason", "no video stream"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate video stream",
			"Ripped file does not contain a video stream",
			nil,
		)
	}
	if probe.AudioStreamCount() == 0 {
		logger.Error("ripping validation failed", logging.String("reason", "no audio stream"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate audio stream",
			"Ripped file does not contain an audio stream",
			nil,
		)
	}
	duration := probe.DurationSeconds()
	if duration <= 0 {
		logger.Error("ripping validation failed", logging.String("reason", "invalid duration"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate duration",
			"Ripped file duration could not be determined",
			nil,
		)
	}

	logger.Info("ripping validation decision",
		logging.String(logging.FieldDecisionType, "rip_validation"),
		logging.String("decision_result", "passed"),
		logging.String("decision_reason", "valid_media_file"),
		logging.String("ripped_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.Float64("duration_seconds", duration),
		logging.Int("video_streams", probe.VideoStreamCount()),
		logging.Int("audio_streams", probe.AudioStreamCount()),
	)

	return nil
}
