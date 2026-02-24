package subtitles

import (
	"context"
	"fmt"
	"strings"

	"log/slog"

	"spindle/internal/deps"
	langpkg "spindle/internal/language"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services"
)

// muxValidationResult captures the outcome of subtitle muxing verification.
type muxValidationResult struct {
	SubtitleCount        int
	RegularMarkedDefault bool     // True if any regular (non-forced) track has the default flag (bad)
	ForcedMarkedDefault  bool     // True if a forced track has the default flag (good)
	LanguageMatch        bool     // True if language metadata matches expected
	HasRegularSubs       bool     // True if non-forced subtitles exist
	HasForcedSubs        bool     // True if forced subtitles exist
	LabelIssues          []string // Label validation problems (missing or incorrect titles)
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

	// Regular subs must NOT be marked default (Jellyfin Android TV auto-displays them).
	// Forced subs SHOULD be marked default so players auto-select them.
	if result.RegularMarkedDefault {
		issues = append(issues, "regular subtitle track has default flag set (causes unwanted auto-display in Jellyfin Android TV)")
	}
	if result.HasForcedSubs && !result.ForcedMarkedDefault {
		issues = append(issues, "forced subtitle track is not marked as default (players may not auto-select it)")
	}

	// Verify language metadata if expected language was provided
	if expectedLang != "" && !result.LanguageMatch {
		issues = append(issues, fmt.Sprintf(
			"subtitle language metadata does not match expected %q",
			expectedLang,
		))
	}

	// Include any label validation issues
	issues = append(issues, result.LabelIssues...)

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
			logging.Bool("labels_verified", true),
		)
	}

	return nil
}

// analyzeSubtitleStreams examines subtitle streams and returns validation metrics.
func analyzeSubtitleStreams(streams []ffprobe.Stream, expectedLang string) muxValidationResult {
	var result muxValidationResult

	expectedLang = strings.ToLower(strings.TrimSpace(expectedLang))
	expectedLang3 := langpkg.ToISO3(expectedLang)
	expectedLangName := langpkg.DisplayName(expectedLang)

	for _, stream := range streams {
		if stream.CodecType != "subtitle" {
			continue
		}

		result.SubtitleCount++

		isForced := stream.Disposition != nil && stream.Disposition["forced"] == 1

		// Check disposition flags
		if stream.Disposition != nil {
			hasDefault := stream.Disposition["default"] == 1
			if isForced {
				result.HasForcedSubs = true
				if hasDefault {
					result.ForcedMarkedDefault = true
				}
			} else {
				result.HasRegularSubs = true
				if hasDefault {
					result.RegularMarkedDefault = true
				}
			}
		} else {
			// No disposition info, assume regular subtitle
			result.HasRegularSubs = true
		}

		// Check language metadata
		if !result.LanguageMatch && expectedLang != "" {
			lang := langpkg.ExtractFromTags(stream.Tags)
			if lang == expectedLang || lang == expectedLang3 {
				result.LanguageMatch = true
			}
		}

		// Check track label/title
		title := subtitleTitle(stream.Tags)
		if title == "" {
			result.LabelIssues = append(result.LabelIssues, fmt.Sprintf(
				"subtitle stream %d has no title label",
				stream.Index,
			))
		} else if isForced {
			// Forced subtitles should have "(Forced)" in the title
			if !strings.Contains(title, "(Forced)") {
				result.LabelIssues = append(result.LabelIssues, fmt.Sprintf(
					"forced subtitle stream %d has title %q but lacks '(Forced)' label",
					stream.Index, title,
				))
			}
		} else if expectedLangName != "" && expectedLangName != "Unknown" {
			// Regular subtitles should have the language name in the title
			if !strings.Contains(strings.ToLower(title), strings.ToLower(expectedLangName)) {
				result.LabelIssues = append(result.LabelIssues, fmt.Sprintf(
					"subtitle stream %d has title %q but expected language %q in label",
					stream.Index, title, expectedLangName,
				))
			}
		}
	}

	// If no language check was requested, mark as matching
	if expectedLang == "" {
		result.LanguageMatch = true
	}

	return result
}

// subtitleTitle extracts the title from subtitle stream tags.
func subtitleTitle(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"title", "TITLE"} {
		if value, ok := tags[key]; ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
