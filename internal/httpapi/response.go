package httpapi

import (
	"encoding/json"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// ItemResponse is the API representation of a queue item (spec Section 2.7).
type ItemResponse struct {
	ID                      int64              `json:"id"`
	DiscTitle               string             `json:"discTitle"`
	Stage                   string             `json:"stage"`
	InProgress              bool               `json:"inProgress"`
	FailedAtStage           string             `json:"failedAtStage,omitempty"`
	ErrorMessage            string             `json:"errorMessage,omitempty"`
	CreatedAt               string             `json:"createdAt"`
	UpdatedAt               string             `json:"updatedAt"`
	DiscFingerprint         string             `json:"discFingerprint,omitempty"`
	NeedsReview             bool               `json:"needsReview"`
	ReviewReason            string             `json:"reviewReason,omitempty"`
	Metadata                json.RawMessage    `json:"metadata,omitempty"`
	RipSpec                 json.RawMessage    `json:"ripSpec,omitempty"`
	ActiveEpisodeKey        string             `json:"activeEpisodeKey,omitempty"`
	Progress                ProgressResponse   `json:"progress"`
	Encoding                json.RawMessage    `json:"encoding,omitempty"`
	Episodes                []EpisodeResponse  `json:"episodes,omitempty"`
	EpisodeTotals           *TotalsResponse    `json:"episodeTotals,omitempty"`
	EpisodeIdentifiedCount  int                `json:"episodeIdentifiedCount,omitempty"`
	SubtitleGeneration      *SubGenResponse    `json:"subtitleGeneration,omitempty"`
	PrimaryAudioDescription string             `json:"primaryAudioDescription,omitempty"`
	CommentaryCount         int                `json:"commentaryCount,omitempty"`
}

// ProgressResponse nests progress fields.
type ProgressResponse struct {
	Stage       string  `json:"stage"`
	Percent     float64 `json:"percent"`
	Message     string  `json:"message"`
	BytesCopied int64   `json:"bytesCopied,omitempty"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
}

// EpisodeResponse represents an episode in the API response.
type EpisodeResponse struct {
	Key                       string            `json:"key"`
	Season                    int               `json:"season"`
	Episode                   int               `json:"episode"`
	Title                     string            `json:"title,omitempty"`
	Stage                     string            `json:"stage"`
	Status                    string            `json:"status,omitempty"`
	ErrorMessage              string            `json:"errorMessage,omitempty"`
	Active                    bool              `json:"active,omitempty"`
	Progress                  *ProgressResponse `json:"progress,omitempty"`
	RuntimeSeconds            int               `json:"runtimeSeconds,omitempty"`
	SourceTitleID             int               `json:"sourceTitleId,omitempty"`
	SourceTitle               string            `json:"sourceTitle,omitempty"`
	OutputBasename            string            `json:"outputBasename,omitempty"`
	RippedPath                string            `json:"rippedPath,omitempty"`
	EncodedPath               string            `json:"encodedPath,omitempty"`
	SubtitledPath             string            `json:"subtitledPath,omitempty"`
	FinalPath                 string            `json:"finalPath,omitempty"`
	GeneratedSubtitleSource   string            `json:"generatedSubtitleSource,omitempty"`
	GeneratedSubtitleLanguage string            `json:"generatedSubtitleLanguage,omitempty"`
	GeneratedSubtitleDecision string            `json:"generatedSubtitleDecision,omitempty"`
	MatchScore                float64           `json:"matchScore,omitempty"`
	MatchedEpisode            int               `json:"matchedEpisode,omitempty"`
}

// TotalsResponse holds per-stage completion counts.
type TotalsResponse struct {
	Planned int `json:"planned"`
	Ripped  int `json:"ripped"`
	Encoded int `json:"encoded"`
	Final   int `json:"final"`
}

// SubGenResponse summarizes subtitle generation.
type SubGenResponse struct {
	OpenSubtitles         int  `json:"opensubtitles"`
	WhisperX              int  `json:"whisperx"`
	ExpectedOpenSubtitles bool `json:"expectedOpenSubtitles"`
	FallbackUsed          bool `json:"fallbackUsed"`
}

// StatusAPIResponse is the top-level /api/status response.
type StatusAPIResponse struct {
	Running      bool           `json:"running"`
	PID          int            `json:"pid"`
	QueueDBPath  string         `json:"queueDbPath"`
	LockFilePath string         `json:"lockFilePath"`
	Workflow     WorkflowStatus `json:"workflow"`
	Dependencies []any          `json:"dependencies"`
}

// WorkflowStatus aggregates queue stats.
type WorkflowStatus struct {
	Running    bool           `json:"running"`
	QueueStats map[string]int `json:"queueStats"`
	LastError  string         `json:"lastError"`
	LastItem   *ItemResponse  `json:"lastItem"`
}

// StatusInfo provides config-derived values needed by the status endpoint.
type StatusInfo struct {
	QueueDBPath  string
	LockFilePath string
}

// NewStatusInfo creates StatusInfo from config.
func NewStatusInfo(cfg *config.Config) StatusInfo {
	return StatusInfo{
		QueueDBPath:  cfg.QueueDBPath(),
		LockFilePath: cfg.LockPath(),
	}
}

// toItemResponse converts a queue.Item to the API response format.
func toItemResponse(item *queue.Item) ItemResponse {
	resp := ItemResponse{
		ID:               item.ID,
		DiscTitle:        item.DiscTitle,
		Stage:            string(item.Stage),
		InProgress:       item.InProgress != 0,
		FailedAtStage:    item.FailedAtStage,
		ErrorMessage:     item.ErrorMessage,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
		DiscFingerprint:  item.DiscFingerprint,
		NeedsReview:      item.NeedsReview != 0,
		ActiveEpisodeKey: item.ActiveEpisodeKey,
		Progress: ProgressResponse{
			Stage:       item.ProgressStage,
			Percent:     item.ProgressPercent,
			Message:     item.ProgressMessage,
			BytesCopied: item.ProgressBytesCopied,
			TotalBytes:  item.ProgressTotalBytes,
		},
	}

	// ReviewReason: DB stores JSON array, spec wants string — flatten to joined string
	if item.ReviewReason != "" {
		var reasons []string
		if err := json.Unmarshal([]byte(item.ReviewReason), &reasons); err == nil && len(reasons) > 0 {
			resp.ReviewReason = strings.Join(reasons, "; ")
		} else {
			resp.ReviewReason = item.ReviewReason
		}
	}

	// MetadataJSON → json.RawMessage
	if item.MetadataJSON != "" {
		resp.Metadata = json.RawMessage(item.MetadataJSON)
	}

	// EncodingDetailsJSON → json.RawMessage (pass through as Snapshot)
	if item.EncodingDetailsJSON != "" {
		resp.Encoding = json.RawMessage(item.EncodingDetailsJSON)
	}

	// Parse RipSpec to compute derived fields
	if item.RipSpecData != "" {
		resp.RipSpec = json.RawMessage(item.RipSpecData)
		env, err := ripspec.Parse(item.RipSpecData)
		if err == nil {
			populateRipSpecDerived(&resp, &env, item)
		}
	}

	return resp
}

// populateRipSpecDerived computes episodes, totals, subtitle generation,
// audio description, etc. from a pre-parsed envelope.
func populateRipSpecDerived(resp *ItemResponse, env *ripspec.Envelope, item *queue.Item) {

	// Episodes
	resp.Episodes = buildEpisodes(env, item)

	// Episode totals
	expected, ripped, encoded, final := env.AssetCounts()
	resp.EpisodeTotals = &TotalsResponse{
		Planned: expected,
		Ripped:  ripped,
		Encoded: encoded,
		Final:   final,
	}

	// Episode identified count
	resp.EpisodeIdentifiedCount = len(env.Episodes) - ripspec.CountUnresolvedEpisodes(env.Episodes)

	// Subtitle generation
	if results := env.Attributes.SubtitleGenerationResults; len(results) > 0 {
		sg := &SubGenResponse{}
		for _, rec := range results {
			switch strings.ToLower(rec.Source) {
			case "opensubtitles":
				sg.OpenSubtitles++
			case "whisperx":
				sg.WhisperX++
			}
		}
		if sg.WhisperX > 0 && sg.OpenSubtitles > 0 {
			sg.FallbackUsed = true
		} else if sg.WhisperX > 0 {
			sg.FallbackUsed = true
			sg.ExpectedOpenSubtitles = true
		}
		resp.SubtitleGeneration = sg
	}

	// Audio analysis
	if aa := env.Attributes.AudioAnalysis; aa != nil {
		resp.PrimaryAudioDescription = aa.PrimaryDescription
		resp.CommentaryCount = len(aa.CommentaryTracks)
	}
}

// buildEpisodes constructs EpisodeResponse slice from envelope data.
func buildEpisodes(env *ripspec.Envelope, item *queue.Item) []EpisodeResponse {
	if len(env.Episodes) == 0 {
		return nil
	}

	titleByID := make(map[int]ripspec.Title, len(env.Titles))
	for _, t := range env.Titles {
		titleByID[t.ID] = t
	}

	// Index subtitle generation results for O(1) lookup per episode.
	subtitleByKey := make(map[string]ripspec.SubtitleGenRecord, len(env.Attributes.SubtitleGenerationResults))
	for _, rec := range env.Attributes.SubtitleGenerationResults {
		subtitleByKey[strings.ToLower(rec.EpisodeKey)] = rec
	}

	episodes := make([]EpisodeResponse, 0, len(env.Episodes))
	for _, ep := range env.Episodes {
		resp := EpisodeResponse{
			Key:            ep.Key,
			Season:         ep.Season,
			Episode:        ep.Episode,
			Title:          ep.EpisodeTitle,
			Stage:          "planned",
			RuntimeSeconds: ep.RuntimeSeconds,
			SourceTitleID:  ep.TitleID,
			OutputBasename: ep.OutputBasename,
			MatchScore:     ep.MatchConfidence,
		}

		if ep.Episode > 0 {
			resp.MatchedEpisode = ep.Episode
		}

		// Title fallback
		if t, ok := titleByID[ep.TitleID]; ok {
			if resp.Title == "" {
				resp.Title = t.EpisodeTitle
			}
			if resp.Title == "" {
				resp.Title = t.Name
			}
			resp.SourceTitle = t.Name
			if resp.RuntimeSeconds == 0 {
				resp.RuntimeSeconds = t.Duration
			}
		}

		// Asset paths and stage progression
		if a, ok := env.Assets.FindAsset("ripped", ep.Key); ok && a.IsCompleted() {
			resp.RippedPath = a.Path
			resp.Stage = "ripped"
		}
		if a, ok := env.Assets.FindAsset("encoded", ep.Key); ok {
			if a.IsCompleted() {
				resp.EncodedPath = a.Path
				resp.Stage = "encoded"
			} else if a.IsFailed() {
				resp.Status = "failed"
				resp.ErrorMessage = a.ErrorMsg
			}
		}
		if a, ok := env.Assets.FindAsset("subtitled", ep.Key); ok {
			if a.IsCompleted() {
				resp.SubtitledPath = a.Path
				resp.Stage = "subtitled"
			} else if a.IsFailed() {
				resp.Status = "failed"
				resp.ErrorMessage = a.ErrorMsg
			}
		}
		if a, ok := env.Assets.FindAsset("final", ep.Key); ok {
			if a.IsCompleted() {
				resp.FinalPath = a.Path
				resp.Stage = "final"
			} else if a.IsFailed() {
				resp.Status = "failed"
				resp.ErrorMessage = a.ErrorMsg
			}
		}

		// Active episode
		if strings.EqualFold(ep.Key, item.ActiveEpisodeKey) {
			resp.Active = true
		}

		// Subtitle generation info per episode
		if rec, ok := subtitleByKey[strings.ToLower(ep.Key)]; ok {
			resp.GeneratedSubtitleSource = rec.Source
			resp.GeneratedSubtitleLanguage = rec.Language
			resp.GeneratedSubtitleDecision = rec.OpenSubtitlesDecision
		}

		episodes = append(episodes, resp)
	}

	return episodes
}
