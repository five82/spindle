package api

import (
	"encoding/json"
	"time"

	"spindle/internal/encodingstate"
)

// dateTimeFormat is used for RFC3339 timestamps in API payloads.
const dateTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// QueueItem describes a queue entry in a transport-friendly format.
type QueueItem struct {
	ID                  int64           `json:"id"`
	DiscTitle           string          `json:"discTitle"`
	SourcePath          string          `json:"sourcePath"`
	Status              string          `json:"status"`
	ProcessingLane      string          `json:"processingLane"`
	Progress            QueueProgress   `json:"progress"`
	Encoding            *EncodingStatus `json:"encoding,omitempty"`
	DraptoPresetProfile string          `json:"draptoPresetProfile,omitempty"`
	ErrorMessage        string          `json:"errorMessage"`
	CreatedAt           string          `json:"createdAt,omitempty"`
	UpdatedAt           string          `json:"updatedAt,omitempty"`
	DiscFingerprint     string          `json:"discFingerprint,omitempty"`
	RippedFile          string          `json:"rippedFile,omitempty"`
	EncodedFile         string          `json:"encodedFile,omitempty"`
	FinalFile           string          `json:"finalFile,omitempty"`
	BackgroundLogPath   string          `json:"backgroundLogPath,omitempty"`
	NeedsReview         bool            `json:"needsReview"`
	ReviewReason        string          `json:"reviewReason,omitempty"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
	RipSpec             json.RawMessage `json:"ripSpec,omitempty"`
	Episodes            []EpisodeStatus `json:"episodes,omitempty"`
	EpisodeTotals       *EpisodeTotals  `json:"episodeTotals,omitempty"`
	EpisodesSynced      bool            `json:"episodesSynchronized,omitempty"`
}

// QueueProgress captures stage progress information for a queue entry.
type QueueProgress struct {
	Stage   string  `json:"stage"`
	Percent float64 `json:"percent"`
	Message string  `json:"message"`
}

// EncodingStatus surfaces Drapto telemetry captured during encoding.
type EncodingStatus = encodingstate.Snapshot

// EpisodeStatus captures the per-episode workflow state for TV discs.
type EpisodeStatus struct {
	Key              string  `json:"key"`
	Season           int     `json:"season"`
	Episode          int     `json:"episode"`
	Title            string  `json:"title"`
	Stage            string  `json:"stage"`
	RuntimeSeconds   int     `json:"runtimeSeconds,omitempty"`
	SourceTitleID    int     `json:"sourceTitleId,omitempty"`
	SourceTitle      string  `json:"sourceTitle,omitempty"`
	OutputBasename   string  `json:"outputBasename,omitempty"`
	RippedPath       string  `json:"rippedPath,omitempty"`
	EncodedPath      string  `json:"encodedPath,omitempty"`
	SubtitledPath    string  `json:"subtitledPath,omitempty"`
	FinalPath        string  `json:"finalPath,omitempty"`
	SubtitleSource   string  `json:"subtitleSource,omitempty"`
	SubtitleLanguage string  `json:"subtitleLanguage,omitempty"`
	MatchScore       float64 `json:"matchScore,omitempty"`
	MatchedEpisode   int     `json:"matchedEpisode,omitempty"`
}

// EpisodeTotals summarizes how far a multi-episode disc progressed.
type EpisodeTotals struct {
	Planned int `json:"planned"`
	Ripped  int `json:"ripped"`
	Encoded int `json:"encoded"`
	Final   int `json:"final"`
}

// WorkflowStatus summarizes workflow execution state.
type WorkflowStatus struct {
	Running     bool           `json:"running"`
	QueueStats  map[string]int `json:"queueStats"`
	LastError   string         `json:"lastError,omitempty"`
	LastItem    *QueueItem     `json:"lastItem,omitempty"`
	StageHealth []StageHealth  `json:"stageHealth"`
}

// StageHealth mirrors readiness reporting for workflow stages.
type StageHealth struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail,omitempty"`
}

// DependencyStatus captures availability of an external dependency.
type DependencyStatus struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Optional    bool   `json:"optional"`
	Available   bool   `json:"available"`
	Detail      string `json:"detail,omitempty"`
}

// DaemonStatus aggregates daemon runtime information for API consumers.
type DaemonStatus struct {
	Running      bool               `json:"running"`
	PID          int                `json:"pid"`
	QueueDBPath  string             `json:"queueDbPath"`
	LockFilePath string             `json:"lockFilePath"`
	Workflow     WorkflowStatus     `json:"workflow"`
	Dependencies []DependencyStatus `json:"dependencies"`
}

// QueueStatsResponse provides a normalized queue stats payload.
type QueueStatsResponse struct {
	Counts map[string]int `json:"counts"`
}

// QueueListResponse wraps a collection of queue items for API responses.
type QueueListResponse struct {
	Items []QueueItem `json:"items"`
}

// QueueItemResponse wraps a single queue item.
type QueueItemResponse struct {
	Item QueueItem `json:"item"`
}

// LogEvent mirrors the daemon log streaming payload.
type LogEvent struct {
	Sequence      uint64            `json:"seq"`
	Timestamp     time.Time         `json:"ts"`
	Level         string            `json:"level"`
	Message       string            `json:"msg"`
	Component     string            `json:"component,omitempty"`
	Stage         string            `json:"stage,omitempty"`
	ItemID        int64             `json:"item_id,omitempty"`
	Lane          string            `json:"lane,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Fields        map[string]string `json:"fields,omitempty"`
	Details       []DetailField     `json:"details,omitempty"`
}

// DetailField mirrors the bullet formatting used by console logs.
type DetailField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// LogStreamResponse batches log events for HTTP clients.
type LogStreamResponse struct {
	Events []LogEvent `json:"events"`
	Next   uint64     `json:"next"`
}

// LogTailResponse returns raw log lines and the next file offset.
type LogTailResponse struct {
	Lines  []string `json:"lines"`
	Offset int64    `json:"offset"`
}
