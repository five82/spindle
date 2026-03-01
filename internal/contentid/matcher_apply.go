package contentid

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

func (m *Matcher) applyMatches(env *ripspec.Envelope, season *tmdb.SeasonDetails, showTitle string, matches []matchResult, progress func(phase string, current, total int, episodeKey string)) {
	if env == nil || season == nil || len(matches) == 0 {
		return
	}
	titleByID := make(map[int]*ripspec.Title, len(env.Titles))
	for idx := range env.Titles {
		titleByID[env.Titles[idx].ID] = &env.Titles[idx]
	}
	episodeByNumber := make(map[int]tmdb.Episode, len(season.Episodes))
	for _, e := range season.Episodes {
		episodeByNumber[e.EpisodeNumber] = e
	}
	for idx, match := range matches {
		target, ok := episodeByNumber[match.TargetEpisode]
		if !ok {
			continue
		}
		if episode := env.EpisodeByKey(match.EpisodeKey); episode != nil {
			episode.Season = target.SeasonNumber
			episode.Episode = target.EpisodeNumber
			episode.EpisodeTitle = strings.TrimSpace(target.Name)
			episode.EpisodeAirDate = strings.TrimSpace(target.AirDate)
			episode.OutputBasename = identification.EpisodeOutputBasename(showTitle, target.SeasonNumber, target.EpisodeNumber)
			episode.MatchConfidence = match.Score
		}
		if title := titleByID[match.TitleID]; title != nil {
			title.Season = target.SeasonNumber
			title.Episode = target.EpisodeNumber
			title.EpisodeTitle = strings.TrimSpace(target.Name)
			title.EpisodeAirDate = strings.TrimSpace(target.AirDate)
		}
		if progress != nil {
			progress(PhaseApply, idx+1, len(matches), match.EpisodeKey)
		}
		m.logger.Debug("content id episode matched",
			logging.String("episode_key", match.EpisodeKey),
			logging.Int("title_id", match.TitleID),
			logging.Int("matched_episode", target.EpisodeNumber),
			logging.Float64("score", match.Score),
			logging.String("episode_title", strings.TrimSpace(target.Name)),
		)
	}
}

func (m *Matcher) attachMatchAttributes(env *ripspec.Envelope, matches []matchResult) {
	if env == nil || len(matches) == 0 {
		return
	}
	payload := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		entry := map[string]any{
			"episode_key":     match.EpisodeKey,
			"title_id":        match.TitleID,
			"matched_episode": match.TargetEpisode,
			"score":           match.Score,
		}
		if match.SubtitleFileID > 0 {
			entry["subtitle_file_id"] = match.SubtitleFileID
		}
		if strings.TrimSpace(match.SubtitleLanguage) != "" {
			entry["subtitle_language"] = match.SubtitleLanguage
		}
		if strings.TrimSpace(match.SubtitleCachePath) != "" {
			entry["subtitle_cache_path"] = match.SubtitleCachePath
		}
		payload = append(payload, entry)
	}
	env.SetAttribute(ripspec.AttrContentIDMatches, payload)
	env.SetAttribute(ripspec.AttrContentIDMethod, "whisperx_opensubtitles")
}

func attachTranscriptPaths(env *ripspec.Envelope, fingerprints []ripFingerprint) {
	if env == nil || len(fingerprints) == 0 {
		return
	}
	paths := make(map[string]string, len(fingerprints))
	for _, fp := range fingerprints {
		if strings.TrimSpace(fp.EpisodeKey) != "" && strings.TrimSpace(fp.Path) != "" {
			paths[strings.ToLower(strings.TrimSpace(fp.EpisodeKey))] = fp.Path
		}
	}
	if len(paths) > 0 {
		env.SetAttribute(ripspec.AttrContentIDTranscripts, paths)
	}
}

func markEpisodesSynchronized(env *ripspec.Envelope) {
	if env == nil {
		return
	}
	env.SetAttribute(ripspec.AttrEpisodesSynchronized, true)
}

func (m *Matcher) updateMetadata(item *queue.Item, matches []matchResult, season int) {
	if item == nil || len(matches) == 0 {
		return
	}
	episodes := make([]int, 0, len(matches))
	for _, match := range matches {
		if match.TargetEpisode > 0 {
			episodes = append(episodes, match.TargetEpisode)
		}
	}
	if len(episodes) == 0 {
		return
	}
	sort.Ints(episodes)
	episodes = slices.Compact(episodes)
	var payload map[string]any
	if strings.TrimSpace(item.MetadataJSON) != "" {
		if err := json.Unmarshal([]byte(item.MetadataJSON), &payload); err != nil {
			payload = make(map[string]any)
		}
	} else {
		payload = make(map[string]any)
	}
	payload["episode_numbers"] = episodes
	if season > 0 {
		payload["season_number"] = season
	}
	payload["media_type"] = "tv"
	data, err := json.Marshal(payload)
	if err != nil {
		m.logger.Warn("failed to encode metadata after content id",
			logging.Error(err),
			logging.String(logging.FieldEventType, "metadata_encode_failed"),
			logging.String(logging.FieldImpact, "episode metadata updates were not persisted"),
			logging.String(logging.FieldErrorHint, "Retry content identification or inspect metadata serialization errors"))
		return
	}
	item.MetadataJSON = string(data)
}
