package api

import (
	"encoding/json"
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

	progressStage := item.ProgressStage
	progressPercent := item.ProgressPercent
	if strings.TrimSpace(progressStage) == "" {
		progressStage = defaultProgressStageForStatus(item.Status)
	}
	if item.Status == queue.StatusCompleted {
		stageLower := strings.ToLower(strings.TrimSpace(progressStage))
		if !item.NeedsReview && !strings.Contains(stageLower, "review") {
			progressStage = "Completed"
		}
		if progressPercent < 100 {
			progressPercent = 100
		}
	}

	dto := QueueItem{
		ID:             item.ID,
		DiscTitle:      item.DiscTitle,
		SourcePath:     item.SourcePath,
		Status:         string(item.Status),
		ProcessingLane: string(queue.LaneForItem(item)),
		Progress: QueueProgress{
			Stage:       progressStage,
			Percent:     progressPercent,
			Message:     item.ProgressMessage,
			BytesCopied: item.ProgressBytesCopied,
			TotalBytes:  item.ProgressTotalBytes,
		},
		ErrorMessage:    item.ErrorMessage,
		DiscFingerprint: item.DiscFingerprint,
		RippedFile:      item.RippedFile,
		EncodedFile:     item.EncodedFile,
		FinalFile:       item.FinalFile,
		ItemLogPath:     item.ItemLogPath,
		NeedsReview:     item.NeedsReview,
		ReviewReason:    item.ReviewReason,
	}
	if snapshot, err := encodingstate.Unmarshal(item.EncodingDetailsJSON); err == nil && !snapshot.IsZero() {
		s := snapshot
		dto.Encoding = &s
	}
	if sg := deriveSubtitleGeneration(item); sg != nil {
		dto.SubtitleGeneration = sg
	}
	if audioDesc, commentaryCount := deriveAudioInfo(item); audioDesc != "" || commentaryCount > 0 {
		dto.PrimaryAudioDescription = audioDesc
		dto.CommentaryCount = commentaryCount
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
		dto.EpisodeIdentifiedCount = countEpisodeIdentified(episodes)
		dto.EpisodesSynced = synced
	}
	return dto
}

func defaultProgressStageForStatus(status queue.Status) string {
	switch status {
	case queue.StatusPending:
		return "Pending"
	case queue.StatusIdentifying:
		return "Identifying"
	case queue.StatusIdentified:
		return "Identified"
	case queue.StatusRipping:
		return "Ripping"
	case queue.StatusRipped:
		return "Ripped"
	case queue.StatusEpisodeIdentifying:
		return "Episode identification"
	case queue.StatusEpisodeIdentified:
		return "Episode identified"
	case queue.StatusEncoding:
		return "Encoding"
	case queue.StatusEncoded:
		return "Encoded"
	case queue.StatusSubtitling:
		return "Subtitling"
	case queue.StatusSubtitled:
		return "Subtitled"
	case queue.StatusOrganizing:
		return "Organizing"
	case queue.StatusCompleted:
		return "Completed"
	case queue.StatusFailed:
		return "Failed"
	default:
		return strings.TrimSpace(string(status))
	}
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
	matches := indexMatches(&env.Attributes)
	generated := indexGeneratedSubtitles(env.Attributes.SubtitleGenerationResults)
	effectiveStage := makeEpisodeStageResolver(item)
	statuses := make([]EpisodeStatus, 0, len(env.Episodes))
	totals := EpisodeTotals{Planned: len(env.Episodes)}
	activeKey := strings.TrimSpace(item.ActiveEpisodeKey)
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
		if activeKey != "" && strings.EqualFold(activeKey, strings.TrimSpace(ep.Key)) {
			status.Active = true
			progress := QueueProgress{
				Stage:   item.ProgressStage,
				Percent: item.ProgressPercent,
				Message: item.ProgressMessage,
			}
			status.Progress = &progress
		}
		if t, ok := titles[ep.TitleID]; ok {
			if status.Title == "" {
				status.Title = strings.TrimSpace(t.EpisodeTitle)
			}
			if status.Title == "" && ep.Episode > 0 {
				// Only fall back to MakeMKV title name for resolved episodes.
				// For placeholders (Episode==0), let Flyer reach OutputBasename
				// which contains a unique disc index.
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
			// Per-episode status and error from asset tracking
			if asset.Status != "" {
				status.Status = asset.Status
			}
			if asset.ErrorMessage != "" {
				status.ErrorMessage = asset.ErrorMessage
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
		if gen, ok := generated[strings.ToLower(strings.TrimSpace(ep.Key))]; ok {
			status.GeneratedSubtitleSource = gen.Source
			status.GeneratedSubtitleLanguage = gen.Language
			status.GeneratedSubtitleDecision = gen.Decision
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
	return statuses, totals, episodesSynced(env.Attributes.EpisodesSynchronized, env.Episodes, item.MetadataJSON)
}

func countEpisodeIdentified(episodes []EpisodeStatus) int {
	identified := 0
	for _, ep := range episodes {
		if ep.MatchedEpisode > 0 || ep.MatchScore > 0 || ep.Episode > 0 {
			identified++
		}
	}
	return identified
}

type episodeAssets struct {
	RippedPath    string
	EncodedPath   string
	SubtitledPath string
	FinalPath     string
	Status        string // Overall episode status (derived from most advanced failed asset)
	ErrorMessage  string // Per-episode error message
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
			case ripspec.AssetKindRipped:
				entry.RippedPath = asset.Path
			case ripspec.AssetKindEncoded:
				entry.EncodedPath = asset.Path
				// Track encoded status/error since it's the first per-episode stage
				if asset.Status != "" {
					entry.Status = asset.Status
				}
				if asset.ErrorMsg != "" {
					entry.ErrorMessage = asset.ErrorMsg
				}
			case ripspec.AssetKindSubtitled:
				entry.SubtitledPath = asset.Path
				// Override with subtitled status if it failed
				if asset.Status == ripspec.AssetStatusFailed {
					entry.Status = asset.Status
					entry.ErrorMessage = asset.ErrorMsg
				}
			case ripspec.AssetKindFinal:
				entry.FinalPath = asset.Path
				// Override with final status if it failed
				if asset.Status == ripspec.AssetStatusFailed {
					entry.Status = asset.Status
					entry.ErrorMessage = asset.ErrorMsg
				}
			}
			lookup[key] = entry
		}
	}
	build(ripspec.AssetKindRipped, assets.Ripped)
	build(ripspec.AssetKindEncoded, assets.Encoded)
	build(ripspec.AssetKindSubtitled, assets.Subtitled)
	build(ripspec.AssetKindFinal, assets.Final)
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

func indexMatches(attrs *ripspec.EnvelopeAttributes) map[string]matchInfo {
	if len(attrs.ContentIDMatches) == 0 {
		return nil
	}
	method := strings.TrimSpace(attrs.ContentIDMethod)
	lookup := make(map[string]matchInfo, len(attrs.ContentIDMatches))
	for _, m := range attrs.ContentIDMatches {
		key := strings.ToLower(strings.TrimSpace(m.EpisodeKey))
		if key == "" {
			continue
		}
		lookup[key] = matchInfo{
			SubtitleSource:   method,
			SubtitleLanguage: strings.TrimSpace(m.SubtitleLanguage),
			Score:            m.Score,
			MatchedEpisode:   m.MatchedEpisode,
		}
	}
	return lookup
}

type generatedSubtitleInfo struct {
	Source   string
	Language string
	Decision string
}

func indexGeneratedSubtitles(records []ripspec.SubtitleGenRecord) map[string]generatedSubtitleInfo {
	if len(records) == 0 {
		return nil
	}
	lookup := make(map[string]generatedSubtitleInfo, len(records))
	for _, r := range records {
		key := strings.ToLower(strings.TrimSpace(r.EpisodeKey))
		if key == "" {
			continue
		}
		lookup[key] = generatedSubtitleInfo{
			Source:   strings.ToLower(strings.TrimSpace(r.Source)),
			Language: strings.ToLower(strings.TrimSpace(r.Language)),
			Decision: strings.TrimSpace(r.OpenSubtitlesDecision),
		}
	}
	return lookup
}

func deriveAudioInfo(item *queue.Item) (string, int) {
	if item == nil || strings.TrimSpace(item.RipSpecData) == "" {
		return "", 0
	}
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return "", 0
	}
	audioDesc := strings.TrimSpace(env.Attributes.PrimaryAudioDescription)
	commentaryCount := 0
	if env.Attributes.AudioAnalysis != nil {
		commentaryCount = len(env.Attributes.AudioAnalysis.CommentaryTracks)
	}
	return audioDesc, commentaryCount
}

func deriveSubtitleGeneration(item *queue.Item) *SubtitleGenerationStatus {
	if item == nil || strings.TrimSpace(item.RipSpecData) == "" {
		return nil
	}
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return nil
	}
	var (
		openSubs int
		whisperx int
		expected bool
		fallback bool
	)
	if summary := env.Attributes.SubtitleGenerationSummary; summary != nil {
		openSubs = summary.OpenSubtitles
		whisperx = summary.WhisperX
		expected = summary.ExpectedOpenSubtitles
		fallback = summary.FallbackUsed
	} else {
		generated := indexGeneratedSubtitles(env.Attributes.SubtitleGenerationResults)
		if len(generated) == 0 {
			return nil
		}
		for _, info := range generated {
			switch strings.ToLower(strings.TrimSpace(info.Source)) {
			case "opensubtitles":
				openSubs++
			case "whisperx":
				whisperx++
			}
		}
	}
	if openSubs == 0 && whisperx == 0 {
		return nil
	}
	return &SubtitleGenerationStatus{
		OpenSubtitles:         openSubs,
		WhisperX:              whisperx,
		ExpectedOpenSubtitles: expected,
		FallbackUsed:          fallback,
	}
}

func episodesSynced(synced bool, episodes []ripspec.Episode, metadataJSON string) bool {
	if synced {
		return true
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
	activeKey := ""
	if item != nil {
		queueStage = item.Status.StageKey()
		activeKey = strings.ToLower(strings.TrimSpace(item.ActiveEpisodeKey))
	}
	return func(status EpisodeStatus) string {
		// Prefer concrete artefacts over inferred status.
		switch {
		case status.FinalPath != "":
			return ripspec.AssetKindFinal
		case status.SubtitledPath != "":
			return ripspec.AssetKindSubtitled
		case status.EncodedPath != "":
			return ripspec.AssetKindEncoded
		case status.RippedPath != "":
			return ripspec.AssetKindRipped
		case queueStage != "":
			if activeKey == "" {
				return queueStage
			}
			if !isPerEpisodeQueueStage(queueStage) {
				return queueStage
			}
			if strings.EqualFold(activeKey, strings.ToLower(strings.TrimSpace(status.Key))) {
				return queueStage
			}
			return "planned"
		default:
			return "planned"
		}
	}
}

func isPerEpisodeQueueStage(queueStage string) bool {
	switch strings.ToLower(strings.TrimSpace(queueStage)) {
	case string(queue.StatusEpisodeIdentifying),
		string(queue.StatusEncoding),
		string(queue.StatusSubtitling),
		string(queue.StatusOrganizing):
		return true
	default:
		return false
	}
}
