package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"spindle/internal/encodingstate"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/stage"
	"spindle/internal/workflow"
)

// FromQueueItem converts a queue record to its API representation.
func FromQueueItem(item *queue.Item) QueueItem {
	if item == nil {
		return QueueItem{}
	}

	dto := QueueItem{
		ID:                  item.ID,
		DiscTitle:           item.DiscTitle,
		SourcePath:          item.SourcePath,
		Status:              string(item.Status),
		ProcessingLane:      string(queue.LaneForItem(item)),
		DraptoPresetProfile: strings.TrimSpace(item.DraptoPresetProfile),
		Progress: QueueProgress{
			Stage:   item.ProgressStage,
			Percent: item.ProgressPercent,
			Message: item.ProgressMessage,
		},
		ErrorMessage:      item.ErrorMessage,
		DiscFingerprint:   item.DiscFingerprint,
		RippedFile:        item.RippedFile,
		EncodedFile:       item.EncodedFile,
		FinalFile:         item.FinalFile,
		BackgroundLogPath: item.BackgroundLogPath,
		NeedsReview:       item.NeedsReview,
		ReviewReason:      item.ReviewReason,
	}
	if snapshot, err := encodingstate.Unmarshal(item.EncodingDetailsJSON); err == nil && !snapshot.IsZero() {
		s := snapshot
		dto.Encoding = &s
	}

	if !item.CreatedAt.IsZero() {
		dto.CreatedAt = item.CreatedAt.UTC().Format(dateTimeFormat)
	}
	if !item.UpdatedAt.IsZero() {
		dto.UpdatedAt = item.UpdatedAt.UTC().Format(dateTimeFormat)
	}
	if raw := item.MetadataJSON; raw != "" {
		dto.Metadata = json.RawMessage(raw)
	}
	if raw := item.RipSpecData; raw != "" {
		dto.RipSpec = json.RawMessage(raw)
	}
	if episodes, totals, synced := deriveEpisodeStatuses(item); len(episodes) > 0 {
		dto.Episodes = episodes
		if totals.Planned > 0 {
			t := totals
			dto.EpisodeTotals = &t
		}
		dto.EpisodesSynced = synced
	}
	return dto
}

// FromQueueItems converts a slice of queue records into API DTOs.
func FromQueueItems(items []*queue.Item) []QueueItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]QueueItem, 0, len(items))
	for _, item := range items {
		out = append(out, FromQueueItem(item))
	}
	return out
}

// FromStatusSummary converts a workflow status summary to API payload.
func FromStatusSummary(summary workflow.StatusSummary) WorkflowStatus {
	healthNames := make([]string, 0, len(summary.StageHealth))
	for name := range summary.StageHealth {
		healthNames = append(healthNames, name)
	}
	slices.Sort(healthNames)

	health := make([]StageHealth, 0, len(healthNames))
	for _, name := range healthNames {
		h := summary.StageHealth[name]
		health = append(health, StageHealth{
			Name:   name,
			Ready:  h.Ready,
			Detail: h.Detail,
		})
	}

	stats := make(map[string]int, len(summary.QueueStats))
	for status, count := range summary.QueueStats {
		stats[string(status)] = count
	}

	wf := WorkflowStatus{
		Running:     summary.Running,
		QueueStats:  stats,
		StageHealth: health,
	}

	if summary.LastError != "" {
		wf.LastError = summary.LastError
	}
	if summary.LastItem != nil {
		last := FromQueueItem(summary.LastItem)
		wf.LastItem = &last
	}
	return wf
}

// MergeQueueStats produces a string-keyed representation of queue stats.
func MergeQueueStats(stats map[queue.Status]int) map[string]int {
	out := make(map[string]int, len(stats))
	for status, count := range stats {
		out[string(status)] = count
	}
	return out
}

// StageHealthSlice converts a stage health map into a deterministic slice.
func StageHealthSlice(health map[string]stage.Health) []StageHealth {
	if len(health) == 0 {
		return nil
	}
	names := make([]string, 0, len(health))
	for name := range health {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]StageHealth, 0, len(names))
	for _, name := range names {
		h := health[name]
		out = append(out, StageHealth{Name: name, Ready: h.Ready, Detail: h.Detail})
	}
	return out
}

// FormatTime converts a time to RFC3339 or returns empty string.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(dateTimeFormat)
}

func deriveEpisodeStatuses(item *queue.Item) ([]EpisodeStatus, EpisodeTotals, bool) {
	if item == nil || strings.TrimSpace(item.RipSpecData) == "" {
		return nil, EpisodeTotals{}, false
	}
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil || len(env.Episodes) == 0 {
		return nil, EpisodeTotals{}, false
	}
	titles := indexTitles(env.Titles)
	assets := indexAssets(env.Assets)
	matches := indexMatches(env.Attributes)
	effectiveStage := makeEpisodeStageResolver(item)
	statuses := make([]EpisodeStatus, 0, len(env.Episodes))
	totals := EpisodeTotals{Planned: len(env.Episodes)}
	for _, ep := range env.Episodes {
		status := EpisodeStatus{
			Key:            ep.Key,
			Season:         ep.Season,
			Episode:        ep.Episode,
			Title:          strings.TrimSpace(ep.EpisodeTitle),
			RuntimeSeconds: ep.RuntimeSeconds,
			SourceTitleID:  ep.TitleID,
			OutputBasename: strings.TrimSpace(ep.OutputBasename),
		}
		if t, ok := titles[ep.TitleID]; ok {
			if status.Title == "" {
				status.Title = strings.TrimSpace(t.EpisodeTitle)
			}
			if status.Title == "" {
				status.Title = strings.TrimSpace(t.Name)
			}
			status.SourceTitle = strings.TrimSpace(t.Name)
			if status.RuntimeSeconds == 0 {
				status.RuntimeSeconds = t.Duration
			}
		}
		if asset, ok := assets[strings.ToLower(ep.Key)]; ok {
			if asset.RippedPath != "" {
				status.RippedPath = asset.RippedPath
				totals.Ripped++
			}
			if asset.EncodedPath != "" {
				status.EncodedPath = asset.EncodedPath
				totals.Encoded++
			}
			if asset.SubtitledPath != "" {
				status.SubtitledPath = asset.SubtitledPath
			}
			if asset.FinalPath != "" {
				status.FinalPath = asset.FinalPath
				totals.Final++
			}
		}
		status.Stage = effectiveStage(status)
		if match, ok := matches[strings.ToLower(ep.Key)]; ok {
			status.SubtitleLanguage = match.SubtitleLanguage
			status.SubtitleSource = match.SubtitleSource
			status.MatchScore = match.Score
			status.MatchedEpisode = match.MatchedEpisode
			if status.Episode == 0 && match.MatchedEpisode > 0 {
				status.Episode = match.MatchedEpisode
			}
		}
		statuses = append(statuses, status)
	}
	slices.SortFunc(statuses, func(a, b EpisodeStatus) int {
		if a.Season != b.Season {
			return a.Season - b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode - b.Episode
		}
		return strings.Compare(a.Key, b.Key)
	})
	return statuses, totals, episodesSynced(env.Attributes, env.Episodes, item.MetadataJSON)
}

type episodeAssets struct {
	RippedPath    string
	EncodedPath   string
	SubtitledPath string
	FinalPath     string
}

func indexAssets(assets ripspec.Assets) map[string]episodeAssets {
	lookup := make(map[string]episodeAssets)
	build := func(kind string, list []ripspec.Asset) {
		for _, asset := range list {
			key := strings.ToLower(strings.TrimSpace(asset.EpisodeKey))
			if key == "" {
				continue
			}
			entry := lookup[key]
			switch kind {
			case "ripped":
				entry.RippedPath = asset.Path
			case "encoded":
				entry.EncodedPath = asset.Path
			case "subtitled":
				entry.SubtitledPath = asset.Path
			case "final":
				entry.FinalPath = asset.Path
			}
			lookup[key] = entry
		}
	}
	build("ripped", assets.Ripped)
	build("encoded", assets.Encoded)
	build("subtitled", assets.Subtitled)
	build("final", assets.Final)
	return lookup
}

type titleInfo struct {
	Name         string
	EpisodeTitle string
	Duration     int
}

func indexTitles(titles []ripspec.Title) map[int]titleInfo {
	if len(titles) == 0 {
		return nil
	}
	lookup := make(map[int]titleInfo, len(titles))
	for _, title := range titles {
		lookup[title.ID] = titleInfo{Name: title.Name, EpisodeTitle: title.EpisodeTitle, Duration: title.Duration}
	}
	return lookup
}

type matchInfo struct {
	SubtitleSource   string
	SubtitleLanguage string
	Score            float64
	MatchedEpisode   int
}

func indexMatches(attrs map[string]any) map[string]matchInfo {
	if len(attrs) == 0 {
		return nil
	}
	var method string
	if raw, ok := attrs["content_id_method"]; ok {
		method = strings.TrimSpace(asString(raw))
	}
	var rawMatches []any
	switch v := attrs["content_id_matches"].(type) {
	case []any:
		rawMatches = v
	case []map[string]any:
		rawMatches = make([]any, len(v))
		for i := range v {
			rawMatches[i] = v[i]
		}
	default:
		return nil
	}
	lookup := make(map[string]matchInfo, len(rawMatches))
	for _, entry := range rawMatches {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(asString(m["episode_key"])))
		if key == "" {
			continue
		}
		lookup[key] = matchInfo{
			SubtitleSource:   method,
			SubtitleLanguage: strings.TrimSpace(asString(m["subtitle_language"])),
			Score:            asFloat(m["score"]),
			MatchedEpisode:   asInt(m["matched_episode"]),
		}
	}
	return lookup
}

func episodesSynced(attrs map[string]any, episodes []ripspec.Episode, metadataJSON string) bool {
	if len(attrs) > 0 {
		if raw, ok := attrs["episodes_synchronized"]; ok {
			if flag, ok2 := raw.(bool); ok2 {
				return flag
			}
		}
	}
	if len(episodes) > 0 {
		synced := true
		for _, ep := range episodes {
			if ep.Season <= 0 || ep.Episode <= 0 {
				synced = false
				break
			}
		}
		if synced {
			return true
		}
	}
	if strings.TrimSpace(metadataJSON) == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &payload); err != nil {
		return false
	}
	val, ok := payload["episode_numbers"].([]any)
	return ok && len(val) > 0
}

func makeEpisodeStageResolver(item *queue.Item) func(EpisodeStatus) string {
	queueStage := ""
	if item != nil {
		queueStage = item.Status.StageKey()
	}
	return func(status EpisodeStatus) string {
		// Prefer concrete artefacts over inferred status.
		switch {
		case status.FinalPath != "":
			return "final"
		case status.SubtitledPath != "":
			return "subtitled"
		case status.EncodedPath != "":
			return "encoded"
		case status.RippedPath != "":
			return "ripped"
		case queueStage != "":
			return queueStage
		default:
			return "planned"
		}
	}
}

func asString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case fmt.Stringer:
		return value.String()
	case float64:
		return fmt.Sprintf("%g", value)
	case int:
		return fmt.Sprintf("%d", value)
	default:
		return ""
	}
}

func asFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case json.Number:
		f, _ := value.Float64()
		return f
	case int:
		return float64(value)
	default:
		return 0
	}
}

func asInt(v any) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	case int:
		return value
	default:
		return 0
	}
}
