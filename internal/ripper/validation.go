package ripper

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/five82/spindle/internal/media/ffprobe"
)

const minRipFileSizeBytes = 10 * 1024 * 1024 // 10 MB

// validateRippedArtifact checks that a ripped file is a valid video.
// Returns nil on success, error describing the validation failure otherwise.
func (h *Handler) validateRippedArtifact(ctx context.Context, path string) error {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return fmt.Errorf("rip validation: empty path")
	}

	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("rip validation: stat %s: %w", clean, err)
	}
	if info.IsDir() {
		return fmt.Errorf("rip validation: %s is a directory, not a file", clean)
	}
	if info.Size() < minRipFileSizeBytes {
		return fmt.Errorf("rip validation: %s is %d bytes (minimum %d)", clean, info.Size(), minRipFileSizeBytes)
	}

	probe, err := ffprobe.Inspect(ctx, "ffprobe", clean)
	if err != nil {
		return fmt.Errorf("rip validation: ffprobe %s: %w", clean, err)
	}
	if probe.VideoStreamCount() == 0 {
		return fmt.Errorf("rip validation: %s has no video streams", clean)
	}
	if probe.AudioStreamCount() == 0 {
		return fmt.Errorf("rip validation: %s has no audio streams", clean)
	}
	if probe.DurationSeconds() <= 0 {
		return fmt.Errorf("rip validation: %s has invalid duration", clean)
	}

	return nil
}
