package audioanalysis

import (
	"context"
	"fmt"
	"strings"

	"log/slog"

	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services"
)

// ValidateCommentaryLabeling verifies that all audio streams with comment disposition
// have proper "Commentary" labeling in their title metadata. This ensures Jellyfin
// will recognize and display the commentary tracks correctly.
func ValidateCommentaryLabeling(ctx context.Context, ffprobeBinary string, targets []string, expectedCount int, logger *slog.Logger) error {
	if expectedCount == 0 {
		return nil // No commentary tracks to validate
	}
	if len(targets) == 0 {
		return nil
	}

	ffprobeBinary = deps.ResolveFFprobePath(ffprobeBinary)

	// Validate first target (commentary layout is consistent across episodes)
	path := strings.TrimSpace(targets[0])
	if path == "" {
		return nil
	}

	probe, err := ffprobe.Inspect(ctx, ffprobeBinary, path)
	if err != nil {
		return services.Wrap(
			services.ErrExternalTool,
			"audioanalysis",
			"validate commentary",
			"Failed to probe file for commentary validation",
			err,
		)
	}

	var issues []string
	commentaryCount := 0

	for _, stream := range probe.Streams {
		if stream.CodecType != "audio" {
			continue
		}

		// Check if stream has comment disposition
		hasCommentDisposition := stream.Disposition != nil && stream.Disposition["comment"] == 1
		if !hasCommentDisposition {
			continue
		}

		commentaryCount++

		// Verify title contains "Commentary" (case-insensitive)
		title := audioTitle(stream.Tags)
		if !strings.Contains(strings.ToLower(title), "commentary") {
			issues = append(issues, fmt.Sprintf(
				"audio stream %d has comment disposition but title %q lacks 'Commentary' label",
				stream.Index, title,
			))
		}
	}

	// Check commentary count matches expectations
	if commentaryCount != expectedCount {
		issues = append(issues, fmt.Sprintf(
			"expected %d commentary track(s) but found %d with comment disposition",
			expectedCount, commentaryCount,
		))
	}

	if len(issues) > 0 {
		if logger != nil {
			logger.Error("commentary labeling validation failed",
				logging.Int("expected_commentary_tracks", expectedCount),
				logging.Int("found_commentary_tracks", commentaryCount),
				logging.Int("issue_count", len(issues)),
				logging.String("issues", strings.Join(issues, "; ")),
				logging.String(logging.FieldEventType, "commentary_validation_failed"),
			)
		}
		return services.Wrap(
			services.ErrValidation,
			"audioanalysis",
			"commentary validation",
			fmt.Sprintf("Commentary labeling validation failed: %s", strings.Join(issues, "; ")),
			nil,
		)
	}

	if logger != nil {
		logger.Info("commentary labeling validation passed",
			logging.String(logging.FieldEventType, "commentary_validation_passed"),
			logging.Int("commentary_tracks_verified", commentaryCount),
		)
	}

	return nil
}
