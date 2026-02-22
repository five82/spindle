package identification

import (
	"strings"

	"log/slog"

	"spindle/internal/disc"
	discfingerprint "spindle/internal/disc/fingerprint"
	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

func buildRipSpecs(logger *slog.Logger, scanResult *disc.ScanResult, episodeMatches map[int]episodeAnnotation, identifiedTitle, fallbackTitle string, discNumber int, metadata map[string]any) ([]ripspec.Title, []ripspec.Episode) {
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
			spec.Episode = annotation.Episode
			spec.EpisodeTitle = annotation.Title
			spec.EpisodeAirDate = annotation.Air

			showLabel := identifiedTitle
			if strings.TrimSpace(showLabel) == "" {
				if value, ok := metadata["title"].(string); ok && strings.TrimSpace(value) != "" {
					showLabel = value
				} else {
					showLabel = fallbackTitle
				}
			}

			if annotation.Episode > 0 {
				// Resolved episode
				episodeSpecs = append(episodeSpecs, ripspec.Episode{
					Key:            ripspec.EpisodeKey(annotation.Season, annotation.Episode),
					TitleID:        t.ID,
					Season:         annotation.Season,
					Episode:        annotation.Episode,
					EpisodeTitle:   annotation.Title,
					EpisodeAirDate: annotation.Air,
					RuntimeSeconds: t.Duration,
					TitleHash:      fp,
					OutputBasename: EpisodeOutputBasename(showLabel, annotation.Season, annotation.Episode),
				})
			} else {
				// Placeholder episode (unresolved number)
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
		if spec.Season > 0 && spec.Episode > 0 {
			logFields = append(logFields,
				logging.Int("season", spec.Season),
				logging.Int("episode", spec.Episode),
				logging.String("episode_title", strings.TrimSpace(spec.EpisodeTitle)))
		}
		logger.Debug("prepared title hash", logFields...)
	}
	return titleSpecs, episodeSpecs
}
