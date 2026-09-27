package spindle

import (
	"encoding/json"
	"strings"
	"time"
)

// StatusResponse mirrors the payload returned by /api/status.
type StatusResponse struct {
	Running      bool               `json:"running"`
	PID          int                `json:"pid"`
	QueueDBPath  string             `json:"queueDbPath"`
	LockFilePath string             `json:"lockFilePath"`
	Workflow     WorkflowStatus     `json:"workflow"`
	Dependencies []DependencyStatus `json:"dependencies"`
	Pipeline     []PipelineStage    `json:"pipeline"`
	Scheduler    *SchedulerStatus   `json:"scheduler"`
	Disc         *DiscStatus        `json:"disc"`
}

// WorkflowStatus aggregates queue stats and the last workflow error.
type WorkflowStatus struct {
	Running    bool           `json:"running"`
	QueueStats map[string]int `json:"queueStats"`
	LastError  string         `json:"lastError"`
}

// PipelineStage describes one stage of the daemon's registered pipeline
// template. This is the source of truth for stage names, display order,
// and dependency edges -- flyer must not hardcode them.
type PipelineStage struct {
	Stage     string   `json:"stage"`
	DependsOn []string `json:"dependsOn"`
	Claims    []string `json:"claims"`
}

// SchedulerStatus reports live resource occupancy.
type SchedulerStatus struct {
	Resources map[string]ResourceStatus `json:"resources"`
}

// ResourceStatus is one resource budget's occupancy.
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

// DiscStatus reports disc-monitor state. The drive is free for another
// disc when the drive resource's Used is 0 and Paused is false.
type DiscStatus struct {
	Paused bool `json:"paused"`
}

// DependencyStatus reports an external dependency health check.
type DependencyStatus struct {
	Name      string `json:"name"`
	Optional  bool   `json:"optional"`
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
}

// QueueListResponse mirrors /api/queue.
type QueueListResponse struct {
	Items []QueueItem `json:"items"`
}

// QueueItem describes a queue entry. Stage is the scheduler's coarse
// position and LAGS running tasks during overlap windows; Tasks carry the
// live truth (state and per-task progress).
type QueueItem struct {
	ID                      int64                     `json:"id"`
	DiscTitle               string                    `json:"discTitle"`
	DisplayTitle            string                    `json:"displayTitle"`
	DiscNumber              int                       `json:"discNumber"`
	Stage                   string                    `json:"stage"`
	FailedAtStage           string                    `json:"failedAtStage"`
	ErrorMessage            string                    `json:"errorMessage"`
	CreatedAt               string                    `json:"createdAt"`
	UpdatedAt               string                    `json:"updatedAt"`
	DiscFingerprint         string                    `json:"discFingerprint"`
	NeedsReview             bool                      `json:"needsReview"`
	UserStopped             bool                      `json:"userStopped"`
	ReviewReasons           []string                  `json:"reviewReasons"`
	Metadata                json.RawMessage           `json:"metadata"`
	Tasks                   []Task                    `json:"tasks"`
	Encoding                *EncodingStatus           `json:"encoding"`
	Episodes                []EpisodeStatus           `json:"episodes"`
	EpisodeIdentifiedCount  int                       `json:"episodeIdentifiedCount"`
	SubtitleGeneration      *SubtitleGenerationStatus `json:"subtitleGeneration"`
	PrimaryAudioDescription string                    `json:"primaryAudioDescription"`
	CommentaryCount         int                       `json:"commentaryCount"`
	ContentID               *ContentID                `json:"contentId"`
	Source                  *SourceTitle              `json:"source"`
}

// Task is one scheduler task of an item, in pipeline order.
type Task struct {
	Type           string       `json:"type"`
	State          string       `json:"state"` // pending, running, done, failed
	Attempts       int          `json:"attempts"`
	Error          string       `json:"error"`
	DependsOn      []string     `json:"dependsOn"`
	StartedAt      string       `json:"startedAt"`
	FinishedAt     string       `json:"finishedAt"`
	Progress       TaskProgress `json:"progress"`
	ActiveAssetKey string       `json:"activeAssetKey"`
}

// TaskProgress is the running task's own progress slot.
type TaskProgress struct {
	Percent     float64 `json:"percent"`
	Message     string  `json:"message"`
	BytesCopied int64   `json:"bytesCopied"`
	TotalBytes  int64   `json:"totalBytes"`
}

// Task state helpers.
func (t Task) IsRunning() bool { return t.State == "running" }
func (t Task) IsDone() bool    { return t.State == "done" }
func (t Task) IsFailed() bool  { return t.State == "failed" }

// ParsedStartedAt returns the task's start time when it parses.
func (t Task) ParsedStartedAt() time.Time { return parseTime(t.StartedAt) }

// ParsedFinishedAt returns the task's finish time when it parses.
func (t Task) ParsedFinishedAt() time.Time { return parseTime(t.FinishedAt) }

// Duration returns the task's run duration when both timestamps parse.
func (t Task) Duration() time.Duration {
	start, end := parseTime(t.StartedAt), parseTime(t.FinishedAt)
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

// RunningTasks returns the item's running tasks in pipeline order.
func (q QueueItem) RunningTasks() []Task {
	var running []Task
	for _, t := range q.Tasks {
		if t.IsRunning() {
			running = append(running, t)
		}
	}
	return running
}

// PrimaryTask picks the task that best represents "what is happening now":
// the first running task, else the first failed task, else the first
// pending task. Returns nil for terminal or task-less items.
func (q QueueItem) PrimaryTask() *Task {
	for _, pick := range []func(Task) bool{Task.IsRunning, Task.IsFailed, func(t Task) bool { return t.State == "pending" }} {
		for i := range q.Tasks {
			if pick(q.Tasks[i]) {
				return &q.Tasks[i]
			}
		}
	}
	return nil
}

// FailedTask returns the first failed task, if any.
func (q QueueItem) FailedTask() *Task {
	for i := range q.Tasks {
		if q.Tasks[i].IsFailed() {
			return &q.Tasks[i]
		}
	}
	return nil
}

// ActiveAssetKeys returns the lowercase asset keys running tasks are
// working on right now.
func (q QueueItem) ActiveAssetKeys() map[string]bool {
	keys := make(map[string]bool)
	for _, t := range q.Tasks {
		if t.IsRunning() && t.ActiveAssetKey != "" {
			keys[strings.ToLower(t.ActiveAssetKey)] = true
		}
	}
	return keys
}

// IsTerminal reports whether the item reached a terminal stage.
func (q QueueItem) IsTerminal() bool {
	return strings.EqualFold(q.Stage, "completed") || strings.EqualFold(q.Stage, "failed")
}

// SubtitleGenerationStatus summarizes generated display subtitles.
type SubtitleGenerationStatus struct {
	WhisperX int `json:"whisperx"`
}

// ContentID summarizes episode-identification provenance.
type ContentID struct {
	Method               string  `json:"method"`
	ReferenceSource      string  `json:"referenceSource"`
	ReferenceEpisodes    int     `json:"referenceEpisodes"`
	TranscribedEpisodes  int     `json:"transcribedEpisodes"`
	MatchedEpisodes      int     `json:"matchedEpisodes"`
	UnresolvedEpisodes   int     `json:"unresolvedEpisodes"`
	LowConfidenceCount   int     `json:"lowConfidenceCount"`
	ReviewThreshold      float64 `json:"reviewThreshold"`
	SequenceContiguous   bool    `json:"sequenceContiguous"`
	EpisodesSynchronized bool    `json:"episodesSynchronized"`
	Completed            bool    `json:"completed"`
}

// SourceTitle summarizes the primary rip-spec title (movie main title).
type SourceTitle struct {
	TitleID         int    `json:"titleId"`
	Name            string `json:"name"`
	DurationSeconds int    `json:"durationSeconds"`
}

// EncodingStatus matches spindle's encodingstate.Snapshot (flat, snake_case JSON).
type EncodingStatus struct {
	Percent               float64             `json:"percent,omitempty"`
	ETASeconds            float64             `json:"eta_seconds,omitempty"`
	FPS                   float64             `json:"fps,omitempty"`
	CurrentFrame          int64               `json:"current_frame,omitempty"`
	TotalFrames           int64               `json:"total_frames,omitempty"`
	CurrentOutputBytes    int64               `json:"current_output_bytes,omitempty"`
	EstimatedTotalBytes   int64               `json:"estimated_total_bytes,omitempty"`
	Substage              string              `json:"substage,omitempty"`
	InputFile             string              `json:"input_file,omitempty"`
	Resolution            string              `json:"resolution,omitempty"`
	DynamicRange          string              `json:"dynamic_range,omitempty"`
	Encoder               string              `json:"encoder,omitempty"`
	Preset                string              `json:"preset,omitempty"`
	Quality               string              `json:"quality,omitempty"`
	Tune                  string              `json:"tune,omitempty"`
	AudioCodec            string              `json:"audio_codec,omitempty"`
	CropFilter            string              `json:"crop_filter,omitempty"`
	CropRequired          bool                `json:"crop_required,omitempty"`
	CropMessage           string              `json:"crop_message,omitempty"`
	OriginalSize          int64               `json:"original_size,omitempty"`
	EncodedSize           int64               `json:"encoded_size,omitempty"`
	SizeReductionPercent  float64             `json:"size_reduction_percent,omitempty"`
	AverageSpeed          float64             `json:"average_speed,omitempty"`
	EncodeDurationSeconds float64             `json:"encode_duration_seconds,omitempty"`
	Warning               string              `json:"warning,omitempty"`
	Error                 *EncodingIssue      `json:"error,omitempty"`
	Validation            *EncodingValidation `json:"validation,omitempty"`
}

type EncodingValidation struct {
	Passed bool                     `json:"passed,omitempty"`
	Steps  []EncodingValidationStep `json:"steps,omitempty"`
}

type EncodingValidationStep struct {
	Name    string `json:"name,omitempty"`
	Passed  bool   `json:"passed,omitempty"`
	Details string `json:"details,omitempty"`
}

type EncodingIssue struct {
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	Context    string `json:"context,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ETADuration returns the ETA as a duration when available.
func (e *EncodingStatus) ETADuration() time.Duration {
	if e == nil || e.ETASeconds <= 0 {
		return 0
	}
	return time.Duration(e.ETASeconds * float64(time.Second))
}

// EpisodeStatus is spindle's per-episode projection. Stage reflects asset
// completion (planned/ripped/encoded/subtitled/final), not the pipeline.
type EpisodeStatus struct {
	Key                  string   `json:"key"`
	Season               int      `json:"season"`
	Episode              int      `json:"episode"`
	EpisodeEnd           int      `json:"episodeEnd"`
	Title                string   `json:"title"`
	Stage                string   `json:"stage"`
	Status               string   `json:"status,omitempty"`       // pending, completed, failed
	ErrorMessage         string   `json:"errorMessage,omitempty"` // per-episode error
	Active               bool     `json:"active,omitempty"`
	RuntimeSeconds       int      `json:"runtimeSeconds"`
	SourceTitleID        int      `json:"sourceTitleId"`
	SourceTitle          string   `json:"sourceTitle"`
	OutputBasename       string   `json:"outputBasename"`
	RippedPath           string   `json:"rippedPath"`
	EncodedPath          string   `json:"encodedPath"`
	SubtitledPath        string   `json:"subtitledPath,omitempty"`
	FinalPath            string   `json:"finalPath"`
	SubtitleSource       string   `json:"subtitleSource"`
	SubtitleLanguage     string   `json:"subtitleLanguage"`
	SubtitleValidation   string   `json:"subtitleValidation"`
	SubtitleReviewIssues []string `json:"subtitleReviewIssues"`
	SubtitleSevereIssues []string `json:"subtitleSevereIssues"`
	CommentaryTracks     int      `json:"commentaryTracks"`
	ExcludedTracks       int      `json:"excludedTracks"`
	MatchScore           float64  `json:"matchScore"`
	MatchConfidence      float64  `json:"matchConfidence"`
	MatchedEpisode       int      `json:"matchedEpisode"`
	NeedsReview          bool     `json:"needsReview"`
	ReviewReason         string   `json:"reviewReason"`
}

// IsFailed returns true if the episode has a failed status.
func (e EpisodeStatus) IsFailed() bool {
	return strings.EqualFold(strings.TrimSpace(e.Status), "failed")
}

// FilterFailed returns episodes with failed status from the given slice.
func FilterFailed(episodes []EpisodeStatus) []EpisodeStatus {
	var failed []EpisodeStatus
	for _, ep := range episodes {
		if ep.IsFailed() {
			failed = append(failed, ep)
		}
	}
	return failed
}

// EpisodeTotals counts per-asset completion, tallied client-side from
// episode paths.
type EpisodeTotals struct {
	Planned   int
	Ripped    int
	Encoded   int
	Subtitled int
	Final     int
}

// EpisodeSnapshot returns the item's episodes with tallied totals.
func (q QueueItem) EpisodeSnapshot() ([]EpisodeStatus, EpisodeTotals) {
	if len(q.Episodes) == 0 {
		return nil, EpisodeTotals{}
	}
	episodes := make([]EpisodeStatus, len(q.Episodes))
	copy(episodes, q.Episodes)
	return episodes, tallyEpisodeTotals(episodes)
}

// LogEvent represents a single log entry from /api/logs.
type LogEvent struct {
	Sequence  uint64            `json:"seq"`
	Timestamp string            `json:"ts"`
	Level     string            `json:"level"`
	Message   string            `json:"msg"`
	Component string            `json:"component"`
	Stage     string            `json:"stage"`
	ItemID    int64             `json:"item_id"`
	Lane      string            `json:"lane"`
	Fields    map[string]string `json:"fields"`
}

// ParsedTime returns the timestamp as time.Time when possible.
func (e LogEvent) ParsedTime() time.Time {
	return parseTime(e.Timestamp)
}

// LogBatch aggregates a slice of log events with the next sequence cursor.
type LogBatch struct {
	Events []LogEvent `json:"events"`
	Next   uint64     `json:"next"`
}

// ParsedCreatedAt returns the parsed CreatedAt timestamp.
func (q QueueItem) ParsedCreatedAt() time.Time {
	return parseTime(q.CreatedAt)
}

// ParsedUpdatedAt returns the parsed UpdatedAt timestamp.
func (q QueueItem) ParsedUpdatedAt() time.Time {
	return parseTime(q.UpdatedAt)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func tallyEpisodeTotals(list []EpisodeStatus) EpisodeTotals {
	var totals EpisodeTotals
	totals.Planned = len(list)
	for _, ep := range list {
		if strings.TrimSpace(ep.RippedPath) != "" {
			totals.Ripped++
		}
		if strings.TrimSpace(ep.EncodedPath) != "" {
			totals.Encoded++
		}
		if strings.TrimSpace(ep.SubtitledPath) != "" {
			totals.Subtitled++
		}
		if strings.TrimSpace(ep.FinalPath) != "" {
			totals.Final++
		}
	}
	return totals
}
