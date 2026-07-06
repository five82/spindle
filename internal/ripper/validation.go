package ripper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

const minRipFileSizeBytes = 10 * 1024 * 1024 // 10 MB

// validateRippedArtifact checks that a ripped file is a valid video and
// returns the ffprobe result on success for reuse by callers (e.g. the
// encode-tier resolution signal). Returns a nil probe and an error
// describing the validation failure otherwise.
func (h *Handler) validateRippedArtifact(ctx context.Context, path string) (*ffprobe.Result, error) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return nil, fmt.Errorf("rip validation: empty path")
	}

	info, err := os.Stat(clean)
	if err != nil {
		return nil, fmt.Errorf("rip validation: stat %s: %w", clean, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("rip validation: %s is a directory, not a file", clean)
	}
	if info.Size() < minRipFileSizeBytes {
		return nil, fmt.Errorf("rip validation: %s is %d bytes (minimum %d)", clean, info.Size(), minRipFileSizeBytes)
	}

	probe, err := ffprobe.Inspect(ctx, "ffprobe", clean)
	if err != nil {
		return nil, fmt.Errorf("rip validation: ffprobe %s: %w", clean, err)
	}
	if probe.VideoStreamCount() == 0 {
		return nil, fmt.Errorf("rip validation: %s has no video streams", clean)
	}
	if probe.AudioStreamCount() == 0 {
		return nil, fmt.Errorf("rip validation: %s has no audio streams", clean)
	}
	if probe.DurationSeconds() <= 0 {
		return nil, fmt.Errorf("rip validation: %s has invalid duration", clean)
	}

	return probe, nil
}

// recordEncodeTierSignal re-stamps env.Metadata.UHD from the first ripped
// video asset's probed resolution -- the authoritative evidence, correcting
// the identification-time stamp from the MakeMKV scan (which covers the
// window where the encode tier claim resolves before ripping finishes) in
// both directions. On probe failure the scan-time stamp is kept unchanged.
func recordEncodeTierSignal(logger *slog.Logger, env *ripspec.Envelope, path string, probe *ffprobe.Result, probeErr error) {
	if probeErr != nil {
		logger.Warn("encode tier rip probe unavailable",
			"event_type", "encode_tier_probe_failed",
			"error_hint", probeErr.Error(),
			"impact", "encode tier claim keeps the identification scan signal",
			"file", path,
		)
		return
	}

	var width, height int
	for _, s := range probe.Streams {
		if s.CodecType == "video" {
			width, height = s.Width, s.Height
			break
		}
	}

	uhd := ripspec.IsUHDResolution(width, height)
	env.Metadata.UHD = uhd
	result := "hd"
	if uhd {
		result = "uhd"
	}
	logger.Info("encode tier resolution signal determined",
		"decision_type", logs.DecisionEncodeTierSignal,
		"decision_result", result,
		"decision_reason", fmt.Sprintf("source=rip_probe resolution=%dx%d file=%s", width, height, filepath.Base(path)),
	)
}
