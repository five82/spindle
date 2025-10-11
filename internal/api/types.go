package api

import "encoding/json"

// dateTimeFormat is used for RFC3339 timestamps in API payloads.
const dateTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// QueueItem describes a queue entry in a transport-friendly format.
type QueueItem struct {
	ID                int64           `json:"id"`
	DiscTitle         string          `json:"discTitle"`
	SourcePath        string          `json:"sourcePath"`
	Status            string          `json:"status"`
	ProcessingLane    string          `json:"processingLane"`
	Progress          QueueProgress   `json:"progress"`
	ErrorMessage      string          `json:"errorMessage"`
	CreatedAt         string          `json:"createdAt,omitempty"`
	UpdatedAt         string          `json:"updatedAt,omitempty"`
	DiscFingerprint   string          `json:"discFingerprint,omitempty"`
	RippedFile        string          `json:"rippedFile,omitempty"`
	EncodedFile       string          `json:"encodedFile,omitempty"`
	FinalFile         string          `json:"finalFile,omitempty"`
	BackgroundLogPath string          `json:"backgroundLogPath,omitempty"`
	NeedsReview       bool            `json:"needsReview"`
	ReviewReason      string          `json:"reviewReason,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
}

// QueueProgress captures stage progress information for a queue entry.
type QueueProgress struct {
	Stage   string  `json:"stage"`
	Percent float64 `json:"percent"`
	Message string  `json:"message"`
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
	DraptoLog    string             `json:"draptoLogPath,omitempty"`
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
