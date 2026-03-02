package identification

import (
	"strings"

	"log/slog"

	"spindle/internal/disc"
	discfingerprint "spindle/internal/disc/fingerprint"
	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

// buildRipSpecs creates title and episode specifications from scan results.
// Episode annotations contain placeholder Episode=0 values because the
// identification stage only determines show and season. The episodeid stage
// later resolves definitive episode numbers via content matching (WhisperX +
// OpenSubtitles).
func buildRipSpecs(logger *slog.Logger, scanResult *disc.ScanResult, episodeMatches map[int]episodeAnnotation, identifiedTitle, fallbackTitle string, discNumber int, metadata ripspec.EnvelopeMetadata) ([]ripspec.Title, []ripspec.Episode) {
	if scanResult == nil {
		return nil, nil
	}
	episodeSpecs := make([]ripspec.Episode, 0, len(episodeMatches))
	titleSpecs := make([]ripspec.Title, 0, len(scanResult.Titles))
	discIndex := 0
	for _, t := range scanResult.Titles {
		fp := discfingerprint.TitleHash(t)
		spec := ripspec.Title{
			ID:           t.ID,
			Name:         t.Name,
			Duration:     t.Duration,
			Chapters:     t.Chapters,
			Playlist:     t.Playlist,
			SegmentCount: t.SegmentCount,
			SegmentMap:   t.SegmentMap,
			TitleHash:    fp,
		}
		if annotation, ok := episodeMatches[t.ID]; ok && annotation.Season > 0 {
			spec.Season = annotation.Season

			showLabel := identifiedTitle
			if strings.TrimSpace(showLabel) == "" {
				if strings.TrimSpace(metadata.Title) != "" {
					showLabel = metadata.Title
				} else {
					showLabel = fallbackTitle
				}
			}

			discIndex++
			episodeSpecs = append(episodeSpecs, ripspec.Episode{
				Key:            ripspec.PlaceholderKey(annotation.Season, discIndex),
				TitleID:        t.ID,
				Season:         annotation.Season,
				Episode:        0,
				RuntimeSeconds: t.Duration,
				TitleHash:      fp,
				OutputBasename: PlaceholderOutputBasename(showLabel, annotation.Season, discNumber, discIndex),
			})
		}
		titleSpecs = append(titleSpecs, spec)
		displayHash := fp
		if len(displayHash) > 12 {
			displayHash = displayHash[:12]
		}
		logFields := []any{
			logging.Int("title_id", t.ID),
			logging.Int("duration_seconds", t.Duration),
			logging.String("title_name", strings.TrimSpace(t.Name)),
			logging.String("title_hash", displayHash),
		}
		if spec.Season > 0 {
			logFields = append(logFields, logging.Int("season", spec.Season))
		}
		logger.Debug("prepared title hash", logFields...)
	}
	return titleSpecs, episodeSpecs
}
