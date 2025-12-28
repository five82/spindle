package encoding

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services"
)

const (
	minEncodedFileSizeBytes = 5 * 1024 * 1024
)

var encodeProbe = ffprobe.Inspect

// validateEncodedOutputs validates all encoded artifacts.
func (e *Encoder) validateEncodedOutputs(ctx context.Context, encodedPaths []string, stageStart time.Time) error {
	for _, path := range encodedPaths {
		if err := e.validateEncodedArtifact(ctx, path, stageStart); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) validateEncodedArtifact(ctx context.Context, path string, startedAt time.Time) error {
	logger := logging.WithContext(ctx, e.logger)
	clean := strings.TrimSpace(path)
	if clean == "" {
		logger.Error("encoding validation failed", logging.String("reason", "empty path"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			"Encoding produced an empty file path",
			nil,
		)
	}
	info, err := os.Stat(clean)
	if err != nil {
		logger.Error("encoding validation failed", logging.String("reason", "stat failure"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			"Failed to stat encoded file",
			err,
		)
	}
	if info.IsDir() {
		logger.Error("encoding validation failed", logging.String("reason", "path is directory"), logging.String("encoded_path", clean))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			"Encoded artifact points to a directory",
			nil,
		)
	}
	if info.Size() < minEncodedFileSizeBytes {
		logger.Error(
			"encoding validation failed",
			logging.String("reason", "file too small"),
			logging.Int64("size_bytes", info.Size()),
		)
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			fmt.Sprintf("Encoded file %q is unexpectedly small (%d bytes)", clean, info.Size()),
			nil,
		)
	}

	binary := "ffprobe"
	if e.cfg != nil {
		binary = deps.ResolveFFprobePath(e.cfg.FFprobeBinary())
	}
	probe, err := encodeProbe(ctx, binary, clean)
	if err != nil {
		logger.Error("encoding validation failed", logging.String("reason", "ffprobe"), logging.Error(err))
		return services.Wrap(
			services.ErrExternalTool,
			"encoding",
			"ffprobe validation",
			"Failed to inspect encoded file with ffprobe",
			err,
		)
	}
	if probe.VideoStreamCount() == 0 {
		logger.Error("encoding validation failed", logging.String("reason", "no video stream"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate video stream",
			"Encoded file does not contain a video stream",
			nil,
		)
	}
	if probe.AudioStreamCount() == 0 {
		logger.Error("encoding validation failed", logging.String("reason", "no audio stream"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate audio stream",
			"Encoded file does not contain an audio stream",
			nil,
		)
	}
	duration := probe.DurationSeconds()
	if duration <= 0 {
		logger.Error("encoding validation failed", logging.String("reason", "invalid duration"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate duration",
			"Encoded file duration could not be determined",
			nil,
		)
	}

	logger.Debug(
		"encoding validation succeeded",
		logging.String("encoded_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.Group("ffprobe",
			logging.Float64("duration_seconds", duration),
			logging.Int("video_streams", probe.VideoStreamCount()),
			logging.Int("audio_streams", probe.AudioStreamCount()),
		),
	)
	return nil
}
