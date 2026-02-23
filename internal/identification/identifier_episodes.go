package identification

import (
	"fmt"
	"strings"

	"spindle/internal/disc"
	discfingerprint "spindle/internal/disc/fingerprint"
	"spindle/internal/queue"
)

// buildPlaceholderAnnotations creates placeholder episode annotations for each
// title that passes isEpisodeRuntime. Episode numbers are left at 0 (unresolved)
// so that the episodeid stage can assign definitive numbers via content matching.
func buildPlaceholderAnnotations(titles []disc.Title, seasonNumber int) map[int]episodeAnnotation {
	if len(titles) == 0 || seasonNumber <= 0 {
		return nil
	}
	out := make(map[int]episodeAnnotation)
	seen := make(map[string]struct{})
	for _, t := range titles {
		if !isEpisodeRuntime(t.Duration) {
			continue
		}
		// Dedup on SegmentMap (m2ts stream identity) when available.
		// Titles sharing a segment_map reference identical content even if
		// playlist metadata differs (different TitleHash). Fall back to
		// TitleHash for DVDs or discs where SegmentMap is absent.
		dedupKey := strings.TrimSpace(t.SegmentMap)
		if dedupKey == "" {
			dedupKey = discfingerprint.TitleHash(t)
		}
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}
		out[t.ID] = episodeAnnotation{
			Season:  seasonNumber,
			Episode: 0,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PlaceholderOutputBasename generates a placeholder filename for an unresolved episode.
// Includes disc number when known to prevent filename collisions across discs.
func PlaceholderOutputBasename(show string, season, discNumber, discIndex int) string {
	show = strings.TrimSpace(show)
	if show == "" {
		show = "Manual Import"
	}
	if discNumber > 0 {
		return fmt.Sprintf("%s - S%02d Disc %d Episode %03d", show, season, discNumber, discIndex)
	}
	return fmt.Sprintf("%s - S%02d Episode %03d", show, season, discIndex)
}

// EpisodeOutputBasename generates a standard episode filename from show, season, and episode.
func EpisodeOutputBasename(show string, season, episode int) string {
	show = strings.TrimSpace(show)
	if show == "" {
		show = "Manual Import"
	}
	display := fmt.Sprintf("%s Season %02d", show, season)
	meta := queue.NewTVMetadata(show, season, []int{episode}, display)
	name := meta.GetFilename()
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("%s - S%02dE%02d", strings.TrimSpace(show), season, episode)
	}
	return name
}
