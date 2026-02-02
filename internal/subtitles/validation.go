package subtitles

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

// muxValidationResult captures the outcome of subtitle muxing verification.
type muxValidationResult struct {
	SubtitleCount  int
	HasDefault     bool // True if at least one track is marked default
	LanguageMatch  bool // True if language metadata matches expected
	HasRegularSubs bool // True if non-forced subtitles exist
	HasForcedSubs  bool // True if forced subtitles exist
}

// ValidateMuxedSubtitles verifies that subtitles were correctly muxed into the MKV.
// This catches silent failures where mkvmerge succeeds but subtitles aren't embedded.
func ValidateMuxedSubtitles(ctx context.Context, ffprobeBinary, mkvPath string, expectedCount int, expectedLang string, logger *slog.Logger) error {
	if expectedCount == 0 {
		return nil // No subtitles expected
	}

	mkvPath = strings.TrimSpace(mkvPath)
	if mkvPath == "" {
		return services.Wrap(
			services.ErrValidation,
			"subtitles",
			"validate mux",
			"MKV path is required for mux validation",
			nil,
		)
	}

	ffprobeBinary = deps.ResolveFFprobePath(ffprobeBinary)

	probe, err := ffprobe.Inspect(ctx, ffprobeBinary, mkvPath)
	if err != nil {
		return services.Wrap(
			services.ErrExternalTool,
			"subtitles",
			"validate mux",
			"Failed to probe MKV for subtitle validation",
			err,
		)
	}

	result := analyzeSubtitleStreams(probe.Streams, expectedLang)

	var issues []string

	// Verify expected number of subtitle tracks
	if result.SubtitleCount != expectedCount {
		issues = append(issues, fmt.Sprintf(
			"expected %d subtitle track(s) but found %d",
			expectedCount, result.SubtitleCount,
		))
	}

	// Verify at least one track is marked default (for regular subs)
	if result.HasRegularSubs && !result.HasDefault {
		issues = append(issues, "no subtitle track marked as default")
	}

	// Verify language metadata if expected language was provided
	if expectedLang != "" && !result.LanguageMatch {
		issues = append(issues, fmt.Sprintf(
			"subtitle language metadata does not match expected %q",
			expectedLang,
		))
	}

	if len(issues) > 0 {
		if logger != nil {
			logger.Error("subtitle mux validation failed",
				logging.String("mkv_path", mkvPath),
				logging.Int("expected_tracks", expectedCount),
				logging.Int("found_tracks", result.SubtitleCount),
				logging.Int("issue_count", len(issues)),
				logging.String("issues", strings.Join(issues, "; ")),
				logging.String(logging.FieldEventType, "subtitle_mux_validation_failed"),
			)
		}
		return services.Wrap(
			services.ErrValidation,
			"subtitles",
			"mux validation",
			fmt.Sprintf("Subtitle mux validation failed: %s", strings.Join(issues, "; ")),
			nil,
		)
	}

	if logger != nil {
		logger.Info("subtitle mux validation passed",
			logging.String(logging.FieldEventType, "subtitle_mux_validation_passed"),
			logging.String("mkv_path", mkvPath),
			logging.Int("subtitle_tracks_verified", result.SubtitleCount),
			logging.Bool("has_regular", result.HasRegularSubs),
			logging.Bool("has_forced", result.HasForcedSubs),
		)
	}

	return nil
}

// analyzeSubtitleStreams examines subtitle streams and returns validation metrics.
func analyzeSubtitleStreams(streams []ffprobe.Stream, expectedLang string) muxValidationResult {
	var result muxValidationResult

	expectedLang = strings.ToLower(strings.TrimSpace(expectedLang))
	expectedLang3 := mapLanguageCode(expectedLang)

	for _, stream := range streams {
		if stream.CodecType != "subtitle" {
			continue
		}

		result.SubtitleCount++

		// Check disposition flags
		if stream.Disposition != nil {
			if stream.Disposition["default"] == 1 {
				result.HasDefault = true
			}
			if stream.Disposition["forced"] == 1 {
				result.HasForcedSubs = true
			} else {
				result.HasRegularSubs = true
			}
		} else {
			// No disposition info, assume regular subtitle
			result.HasRegularSubs = true
		}

		// Check language metadata
		if !result.LanguageMatch && expectedLang != "" {
			lang := normalizeSubtitleLanguage(stream.Tags)
			if lang == expectedLang || lang == expectedLang3 {
				result.LanguageMatch = true
			}
		}
	}

	// If no language check was requested, mark as matching
	if expectedLang == "" {
		result.LanguageMatch = true
	}

	return result
}

// normalizeSubtitleLanguage extracts and normalizes the language from stream tags.
func normalizeSubtitleLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"language", "LANGUAGE"} {
		if value, ok := tags[key]; ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}
