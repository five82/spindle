package httpapi

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// ItemResponse is the HTTP API representation of a queue item. Progress is
// per task (Tasks); the item's Stage is the scheduler's coarse position, not
// a display label -- during overlap windows it lags the running tasks.
// RipSpec (the raw envelope) is included only on single-item GETs.
type ItemResponse struct {
	ID                      int64              `json:"id"`
	DiscTitle               string             `json:"discTitle"`
	DisplayTitle            string             `json:"displayTitle"`
	Stage                   string             `json:"stage"`
	InProgress              bool               `json:"inProgress"`
	FailedAtStage           string             `json:"failedAtStage,omitempty"`
	ErrorMessage            string             `json:"errorMessage,omitempty"`
	CreatedAt               string             `json:"createdAt"`
	UpdatedAt               string             `json:"updatedAt"`
	DiscFingerprint         string             `json:"discFingerprint,omitempty"`
	NeedsReview             bool               `json:"needsReview"`
	UserStopped             bool               `json:"userStopped,omitempty"`
	ReviewReasons           []string           `json:"reviewReasons,omitempty"`
	Metadata                json.RawMessage    `json:"metadata,omitempty"`
	RipSpec                 json.RawMessage    `json:"ripSpec,omitempty"`
	Tasks                   []TaskResponse     `json:"tasks,omitempty"`
	Encoding                json.RawMessage    `json:"encoding,omitempty"`
	Episodes                []EpisodeResponse  `json:"episodes,omitempty"`
	EpisodeTotals           *TotalsResponse    `json:"episodeTotals,omitempty"`
	EpisodeIdentifiedCount  int                `json:"episodeIdentifiedCount,omitempty"`
	SubtitleGeneration      *SubGenResponse    `json:"subtitleGeneration,omitempty"`
	PrimaryAudioDescription string             `json:"primaryAudioDescription,omitempty"`
	CommentaryCount         int                `json:"commentaryCount,omitempty"`
	ContentID               *ContentIDResponse `json:"contentId,omitempty"`
	Source                  *SourceResponse    `json:"source,omitempty"`
}

// SourceResponse summarizes the primary rip-spec title (the movie main
// title; TV clients use per-episode SourceTitle instead), so no client
// needs the raw envelope for it.
type SourceResponse struct {
	TitleID         int    `json:"titleId"`
	Name            string `json:"name,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

// TaskResponse is one scheduler task of an item. DependsOn names task types,
// not row IDs. An item briefly has no tasks while a retry recompiles them.
type TaskResponse struct {
	Type           string           `json:"type"`
	State          string           `json:"state"`
	Attempts       int              `json:"attempts,omitempty"`
	Error          string           `json:"error,omitempty"`
	DependsOn      []string         `json:"dependsOn,omitempty"`
	StartedAt      string           `json:"startedAt,omitempty"`
	FinishedAt     string           `json:"finishedAt,omitempty"`
	Progress       ProgressResponse `json:"progress"`
	ActiveAssetKey string           `json:"activeAssetKey,omitempty"`
}

// ProgressResponse nests a task's progress fields.
type ProgressResponse struct {
	Percent     float64 `json:"percent"`
	Message     string  `json:"message"`
	BytesCopied int64   `json:"bytesCopied,omitempty"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
}

// EpisodeResponse represents an episode in the API response.
type EpisodeResponse struct {
	Key                  string   `json:"key"`
	Season               int      `json:"season"`
	Episode              int      `json:"episode"`
	EpisodeEnd           int      `json:"episodeEnd,omitempty"`
	Title                string   `json:"title,omitempty"`
	Stage                string   `json:"stage"`
	Status               string   `json:"status,omitempty"`
	ErrorMessage         string   `json:"errorMessage,omitempty"`
	Active               bool     `json:"active,omitempty"`
	RuntimeSeconds       int      `json:"runtimeSeconds,omitempty"`
	SourceTitleID        int      `json:"sourceTitleId,omitempty"`
	SourceTitle          string   `json:"sourceTitle,omitempty"`
	OutputBasename       string   `json:"outputBasename,omitempty"`
	RippedPath           string   `json:"rippedPath,omitempty"`
	EncodedPath          string   `json:"encodedPath,omitempty"`
	SubtitledPath        string   `json:"subtitledPath,omitempty"`
	FinalPath            string   `json:"finalPath,omitempty"`
	SubtitleSource       string   `json:"subtitleSource,omitempty"`
	SubtitleLanguage     string   `json:"subtitleLanguage,omitempty"`
	SubtitleValidation   string   `json:"subtitleValidation,omitempty"`
	SubtitleReviewIssues []string `json:"subtitleReviewIssues,omitempty"`
	SubtitleSevereIssues []string `json:"subtitleSevereIssues,omitempty"`
	CommentaryTracks     int      `json:"commentaryTracks,omitempty"`
	ExcludedTracks       int      `json:"excludedTracks,omitempty"`
	MatchScore           float64  `json:"matchScore,omitempty"`
	MatchConfidence      float64  `json:"matchConfidence,omitempty"`
	MatchedEpisode       int      `json:"matchedEpisode,omitempty"`
	MatchedEpisodeEnd    int      `json:"matchedEpisodeEnd,omitempty"`
	NeedsReview          bool     `json:"needsReview,omitempty"`
	ReviewReason         string   `json:"reviewReason,omitempty"`
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
	WhisperX int `json:"whisperx"`
}

// ContentIDResponse mirrors the envelope's episode-identification summary so
// clients never parse the raw rip spec for it.
type ContentIDResponse struct {
	Method               string  `json:"method,omitempty"`
	ReferenceSource      string  `json:"referenceSource,omitempty"`
	ReferenceEpisodes    int     `json:"referenceEpisodes,omitempty"`
	TranscribedEpisodes  int     `json:"transcribedEpisodes,omitempty"`
	MatchedEpisodes      int     `json:"matchedEpisodes,omitempty"`
	UnresolvedEpisodes   int     `json:"unresolvedEpisodes,omitempty"`
	LowConfidenceCount   int     `json:"lowConfidenceCount,omitempty"`
	ReviewThreshold      float64 `json:"reviewThreshold,omitempty"`
	SequenceContiguous   bool    `json:"sequenceContiguous,omitempty"`
	EpisodesSynchronized bool    `json:"episodesSynchronized,omitempty"`
	Completed            bool    `json:"completed,omitempty"`
}

// StatusAPIResponse is the top-level /api/status response.
type StatusAPIResponse struct {
	Running      bool                 `json:"running"`
	PID          int                  `json:"pid"`
	QueueDBPath  string               `json:"queueDbPath"`
	LockFilePath string               `json:"lockFilePath"`
	Workflow     WorkflowStatus       `json:"workflow"`
	Dependencies []DependencyResponse `json:"dependencies"`
	Pipeline     []PipelineStageInfo  `json:"pipeline,omitempty"`
	Scheduler    *SchedulerStatus     `json:"scheduler,omitempty"`
	Disc         *DiscStatus          `json:"disc,omitempty"`
}

// PipelineStageInfo describes one stage of the registered pipeline template,
// so clients render the DAG data-driven instead of hardcoding it.
type PipelineStageInfo struct {
	Stage     string   `json:"stage"`
	DependsOn []string `json:"dependsOn,omitempty"`
	Claims    []string `json:"claims,omitempty"`
}

// SchedulerStatus reports live resource occupancy.
type SchedulerStatus struct {
	Resources map[string]ResourceStatus `json:"resources"`
}

// ResourceStatus is one resource budget's occupancy. The drive is free for
// another disc when the drive resource's Used is 0 and the disc monitor is
// not paused.
type ResourceStatus struct {
	Capacity int              `json:"capacity"`
	Used     int              `json:"used"`
	Holders  []ResourceHolder `json:"holders"`
}

// ResourceHolder names the task currently holding (part of) a resource.
type ResourceHolder struct {
	ItemID int64  `json:"itemId"`
	Task   string `json:"task"`
}

// DiscStatus reports disc-monitor state.
type DiscStatus struct {
	Paused bool `json:"paused"`
}

// SchedulerSource exposes the workflow manager's resource occupancy to the
// status endpoint without an import cycle.
type SchedulerSource interface {
	SchedulerSnapshot() map[string]ResourceStatus
}

// DependencyResponse reports an external dependency health check.
type DependencyResponse struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Optional    bool   `json:"optional"`
	Available   bool   `json:"available"`
	Detail      string `json:"detail,omitempty"`
}

// WorkflowStatus aggregates queue stats. QueueStats counts item stages,
// which lag running tasks during overlap windows; task-level truth is on
// each item's Tasks.
type WorkflowStatus struct {
	Running    bool           `json:"running"`
	QueueStats map[string]int `json:"queueStats"`
	LastError  string         `json:"lastError"`
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

// StatusTracker tracks the last workflow error and dependency status.
// It is goroutine-safe.
type StatusTracker struct {
	mu           sync.RWMutex
	lastError    string
	dependencies []DependencyResponse
}

// NewStatusTracker creates a StatusTracker with the given dependency results.
func NewStatusTracker(deps []DependencyResponse) *StatusTracker {
	return &StatusTracker{dependencies: deps}
}

// RecordSuccess clears the last error after a successful stage.
func (t *StatusTracker) RecordSuccess() {
	t.mu.Lock()
	t.lastError = ""
	t.mu.Unlock()
}

// RecordFailure records a stage failure message.
func (t *StatusTracker) RecordFailure(errMsg string) {
	t.mu.Lock()
	t.lastError = errMsg
	t.mu.Unlock()
}

// Snapshot returns the current status tracking state.
func (t *StatusTracker) Snapshot() (lastError string, deps []DependencyResponse) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastError, t.dependencies
}

// toItemResponse converts a queue.Item and its task rows to the API
// response format. includeRipSpec attaches the raw envelope (single-item
// GETs only: the list endpoint would ship every envelope on every poll).
func toItemResponse(item *queue.Item, tasks []*queue.Task, includeRipSpec bool) ItemResponse {
	resp := ItemResponse{
		ID:              item.ID,
		DiscTitle:       item.DiscTitle,
		DisplayTitle:    item.DisplayTitle(),
		Stage:           string(item.Stage),
		InProgress:      item.InProgress != 0,
		FailedAtStage:   string(item.FailedAtStage),
		ErrorMessage:    item.ErrorMessage,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		DiscFingerprint: item.DiscFingerprint,
		NeedsReview:     item.NeedsReview != 0,
		UserStopped:     item.UserStopped(),
		ReviewReasons:   item.ReviewReasons(),
		Tasks:           toTaskResponses(tasks),
	}

	// MetadataJSON -> json.RawMessage
	if item.MetadataJSON != "" {
		resp.Metadata = json.RawMessage(item.MetadataJSON)
	}

	// EncodingDetailsJSON -> json.RawMessage (pass through as snapshot JSON)
	if item.EncodingDetailsJSON != "" {
		resp.Encoding = json.RawMessage(item.EncodingDetailsJSON)
	}

	// Parse RipSpec to compute derived fields.
	if item.RipSpecData != "" {
		if includeRipSpec {
			resp.RipSpec = json.RawMessage(item.RipSpecData)
		}
		env, err := ripspec.Parse(item.RipSpecData)
		if err == nil {
			populateRipSpecDerived(&resp, &env, activeAssetKeys(tasks))
		}
	}

	return resp
}

// toTaskResponses maps task rows to the API shape, resolving dependency row
// IDs to task types.
func toTaskResponses(tasks []*queue.Task) []TaskResponse {
	if len(tasks) == 0 {
		return nil
	}
	typeByID := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		typeByID[t.ID] = string(t.Type)
	}
	out := make([]TaskResponse, 0, len(tasks))
	for _, t := range tasks {
		tr := TaskResponse{
			Type:       string(t.Type),
			State:      string(t.State),
			Attempts:   t.Attempts,
			Error:      t.ErrorMsg,
			StartedAt:  t.StartedAt,
			FinishedAt: t.FinishedAt,
			Progress: ProgressResponse{
				Percent:     t.ProgressPercent,
				Message:     t.ProgressMessage,
				BytesCopied: t.ProgressBytesCopied,
				TotalBytes:  t.ProgressTotalBytes,
			},
			ActiveAssetKey: t.ActiveAssetKey,
		}
		for _, dep := range t.Deps {
			if name, ok := typeByID[dep]; ok {
				tr.DependsOn = append(tr.DependsOn, name)
			}
		}
		out = append(out, tr)
	}
	return out
}

// activeAssetKeys collects the asset keys running tasks are working on.
func activeAssetKeys(tasks []*queue.Task) map[string]bool {
	keys := make(map[string]bool)
	for _, t := range tasks {
		if t.State == queue.TaskRunning && t.ActiveAssetKey != "" {
			keys[strings.ToLower(t.ActiveAssetKey)] = true
		}
	}
	return keys
}

// populateRipSpecDerived computes episodes, totals, subtitle generation,
// audio description, etc. from a pre-parsed envelope.
func populateRipSpecDerived(resp *ItemResponse, env *ripspec.Envelope, activeKeys map[string]bool) {

	// Episodes
	resp.Episodes = buildEpisodes(env, activeKeys)

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
			if strings.EqualFold(rec.Source, "whisperx") {
				sg.WhisperX++
			}
		}
		resp.SubtitleGeneration = sg
	}

	// Audio analysis
	if aa := env.Attributes.AudioAnalysis; aa != nil {
		resp.PrimaryAudioDescription = aa.PrimaryDescription
		resp.CommentaryCount = len(aa.CommentaryTracks)
	}

	// Primary source title (movie main title)
	if len(env.Titles) > 0 {
		t := env.Titles[0]
		resp.Source = &SourceResponse{
			TitleID:         t.ID,
			Name:            t.Name,
			DurationSeconds: t.Duration,
		}
	}

	// Episode identification provenance
	if cid := env.Attributes.ContentID; cid != nil {
		resp.ContentID = &ContentIDResponse{
			Method:               cid.Method,
			ReferenceSource:      cid.ReferenceSource,
			ReferenceEpisodes:    cid.ReferenceEpisodes,
			TranscribedEpisodes:  cid.TranscribedEpisodes,
			MatchedEpisodes:      cid.MatchedEpisodes,
			UnresolvedEpisodes:   cid.UnresolvedEpisodes,
			LowConfidenceCount:   cid.LowConfidenceCount,
			ReviewThreshold:      cid.ReviewThreshold,
			SequenceContiguous:   cid.SequenceContiguous,
			EpisodesSynchronized: cid.EpisodesSynchronized,
			Completed:            cid.Completed,
		}
	}
}

// buildEpisodes constructs EpisodeResponse slice from envelope data.
// activeKeys marks episodes a running task is currently working on.
func buildEpisodes(env *ripspec.Envelope, activeKeys map[string]bool) []EpisodeResponse {
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
			Key:             ep.Key,
			Season:          ep.Season,
			Episode:         ep.Episode,
			EpisodeEnd:      ep.EpisodeEnd,
			Title:           ep.EpisodeTitle,
			Stage:           "planned",
			RuntimeSeconds:  ep.RuntimeSeconds,
			SourceTitleID:   ep.TitleID,
			OutputBasename:  ep.OutputBasename,
			MatchScore:      ep.MatchScore,
			MatchConfidence: ep.MatchConfidence,
			NeedsReview:     ep.NeedsReview,
			ReviewReason:    ep.ReviewReason,
		}

		if ep.Episode > 0 {
			resp.MatchedEpisode = ep.Episode
			resp.MatchedEpisodeEnd = ep.EpisodeEnd
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
		if a, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, ep.Key); ok && a.IsCompleted() {
			resp.RippedPath = a.Path
			resp.Stage = "ripped"
		}
		if a, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, ep.Key); ok {
			if a.IsCompleted() {
				resp.EncodedPath = a.Path
				resp.Stage = "encoded"
			} else if a.IsFailed() {
				resp.Status = "failed"
				resp.ErrorMessage = a.ErrorMsg
			}
		}
		if a, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, ep.Key); ok {
			if a.IsCompleted() {
				resp.SubtitledPath = a.Path
				resp.Stage = "subtitled"
			} else if a.IsFailed() {
				resp.Status = "failed"
				resp.ErrorMessage = a.ErrorMsg
			}
		}
		if a, ok := env.Assets.FindAsset(ripspec.AssetKindFinal, ep.Key); ok {
			if a.IsCompleted() {
				resp.FinalPath = a.Path
				resp.Stage = "final"
			} else if a.IsFailed() {
				resp.Status = "failed"
				resp.ErrorMessage = a.ErrorMsg
			}
		}

		// Active episode: any running task working this key.
		if activeKeys[strings.ToLower(ep.Key)] {
			resp.Active = true
		}

		// Subtitle generation info and QC per episode
		if rec, ok := subtitleByKey[strings.ToLower(ep.Key)]; ok {
			resp.SubtitleSource = rec.Source
			resp.SubtitleLanguage = rec.Language
			resp.SubtitleValidation = rec.ValidationResult
			resp.SubtitleReviewIssues = rec.ReviewIssues
			resp.SubtitleSevereIssues = rec.SevereIssues
		}

		// Per-episode audio analysis
		if epAA := env.Attributes.AudioAnalysis.EpisodeAnalysis(ep.Key); epAA != nil {
			resp.CommentaryTracks = len(epAA.CommentaryTracks)
			resp.ExcludedTracks = len(epAA.ExcludedTracks)
		}

		episodes = append(episodes, resp)
	}

	return episodes
}
