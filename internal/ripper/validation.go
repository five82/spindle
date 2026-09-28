package ripper

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/media/ffprobe"
)

const minRipFileSizeBytes = 10 * 1024 * 1024 // 10 MB

// validateRippedArtifact checks that a ripped file is a valid video, returning
// an error describing the validation failure otherwise.
func (h *Handler) validateRippedArtifact(ctx context.Context, path string, expectedSeconds int) error {
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
	if expectedSeconds > 0 {
		// Matroska's duration can follow audio long after the video ends.
		// Check packets rather than container or stream duration.
		out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
			"-show_entries", "packet=pts_time", "-of", "csv=p=0", clean).Output()
		if err != nil {
			return fmt.Errorf("rip validation: video packets %s: %w", clean, err)
		}
		var first, last float64
		seen := false
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			pts, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
			if err != nil {
				continue
			}
			if !seen || pts < first {
				first = pts
			}
			if !seen || pts > last {
				last = pts
			}
			seen = true
		}
		if !seen || last-first < float64(expectedSeconds)*0.9 {
			return fmt.Errorf("rip validation: video spans %.1fs, expected about %ds (container %.1fs): %s",
				last-first, expectedSeconds, probe.DurationSeconds(), clean)
		}
	}
	return nil
}
