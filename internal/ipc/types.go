package ipc

import "spindle/internal/api"

// StartRequest triggers daemon workflow startup.
type StartRequest struct{}

// StartResponse indicates whether the daemon was started.
type StartResponse struct {
	Started bool   `json:"started"`
	Message string `json:"message"`
}

// StopRequest stops daemon workflow.
type StopRequest struct{}

// StopResponse indicates stop result.
type StopResponse struct {
	Stopped bool `json:"stopped"`
}

// StatusRequest fetches daemon status.
type StatusRequest struct{}

// QueueItem mirrors the HTTP API queue DTO for internal IPC callers.
type QueueItem = api.QueueItem

// StageHealth describes readiness of a workflow stage.
type StageHealth = api.StageHealth

// DependencyStatus describes availability of an external dependency.
type DependencyStatus = api.DependencyStatus

// StatusResponse represents combined daemon/workflow status information.
type StatusResponse struct {
	Running           bool                  `json:"running"`
	DiscPaused        bool                  `json:"disc_paused"`
	NetlinkMonitoring bool                  `json:"netlink_monitoring"`
	QueueStats        map[string]int        `json:"queue_stats"`
	SystemChecks      []api.StatusLine      `json:"system_checks,omitempty"`
	LibraryPaths      []api.StatusLine      `json:"library_paths,omitempty"`
	DependencySummary api.DependencySummary `json:"dependency_summary"`
	LastError         string                `json:"last_error"`
	LastItem          *QueueItem            `json:"last_item"`
	LockPath          string                `json:"lock_path"`
	QueueDBPath       string                `json:"queue_db_path"`
	StageHealth       []StageHealth         `json:"stage_health"`
	Dependencies      []DependencyStatus    `json:"dependencies"`
	PID               int                   `json:"pid"`
}

// QueueListRequest filters queue listing by status.
type QueueListRequest struct {
	Statuses []string `json:"statuses"`
}

// QueueListResponse contains queue entries.
type QueueListResponse struct {
	Items []QueueItem `json:"items"`
}

// QueueDescribeRequest fetches a single queue item by id.
type QueueDescribeRequest struct {
	ID int64 `json:"id"`
}

// QueueDescribeResponse contains a single queue entry.
type QueueDescribeResponse struct {
	Found bool      `json:"found"`
	Item  QueueItem `json:"item"`
}

// QueueClearRequest removes all items.
type QueueClearRequest struct{}

// QueueClearResponse reports number of removed entries.
type QueueClearResponse struct {
	Removed int64 `json:"removed"`
}

// QueueClearFailedRequest removes failed items.
type QueueClearFailedRequest struct{}

// QueueClearFailedResponse reports number of removed entries.
type QueueClearFailedResponse struct {
	Removed int64 `json:"removed"`
}

// QueueClearCompletedRequest removes completed items.
type QueueClearCompletedRequest struct{}

// QueueClearCompletedResponse reports number of removed entries.
type QueueClearCompletedResponse struct {
	Removed int64 `json:"removed"`
}

// QueueResetRequest resets in-flight items.
type QueueResetRequest struct{}

// QueueResetResponse reports number of items reset.
type QueueResetResponse struct {
	Updated int64 `json:"updated"`
}

// QueueRetryRequest retries failed items. Empty list means all failed items.
type QueueRetryRequest struct {
	IDs []int64 `json:"ids"`
}

// QueueRetryResponse reports number of retried items.
type QueueRetryResponse struct {
	Updated int64 `json:"updated"`
}

// QueueRetryEpisodeRequest retries a single failed episode within an item.
type QueueRetryEpisodeRequest struct {
	ID         int64  `json:"id"`
	EpisodeKey string `json:"episode_key"`
}

// QueueRetryEpisodeResponse reports per-episode retry outcome.
type QueueRetryEpisodeResponse struct {
	Result api.RetryItemResult `json:"result"`
}

// QueueStopRequest stops queue items. Empty list is invalid.
type QueueStopRequest struct {
	IDs []int64 `json:"ids"`
}

// QueueStopResponse reports number of stopped items.
type QueueStopResponse struct {
	Updated int64 `json:"updated"`
}

// LogTailRequest fetches log lines based on offset and follow semantics.
type LogTailRequest struct {
	Offset     int64 `json:"offset"`
	Limit      int   `json:"limit"`
	Follow     bool  `json:"follow"`
	WaitMillis int   `json:"wait_millis"`
}

// LogTailResponse returns log lines and the next offset.
type LogTailResponse struct {
	Lines  []string `json:"lines"`
	Offset int64    `json:"offset"`
}

// DatabaseHealthRequest fetches detailed database diagnostics.
type DatabaseHealthRequest struct{}

// DatabaseHealthResponse reports database health information.
type DatabaseHealthResponse struct {
	DBPath           string   `json:"db_path"`
	DatabaseExists   bool     `json:"database_exists"`
	DatabaseReadable bool     `json:"database_readable"`
	SchemaVersion    string   `json:"schema_version"`
	TableExists      bool     `json:"table_exists"`
	ColumnsPresent   []string `json:"columns_present"`
	MissingColumns   []string `json:"missing_columns"`
	IntegrityCheck   bool     `json:"integrity_check"`
	TotalItems       int      `json:"total_items"`
	Error            string   `json:"error"`
}

// TestNotificationRequest triggers a notification test.
type TestNotificationRequest struct{}

// TestNotificationResponse reports notification test outcome.
type TestNotificationResponse struct {
	Sent    bool   `json:"sent"`
	Message string `json:"message"`
}

// QueueRemoveRequest removes specific items by ID.
type QueueRemoveRequest struct {
	IDs []int64 `json:"ids"`
}

// QueueRemoveResponse reports number of removed entries.
type QueueRemoveResponse struct {
	Removed int64 `json:"removed"`
}

// DiscPauseRequest pauses detection of new disc insertions.
type DiscPauseRequest struct{}

// DiscPauseResponse reports whether disc processing was paused.
type DiscPauseResponse struct {
	Paused  bool   `json:"paused"`
	Message string `json:"message"`
}

// DiscResumeRequest resumes detection of new disc insertions.
type DiscResumeRequest struct{}

// DiscResumeResponse reports whether disc processing was resumed.
type DiscResumeResponse struct {
	Resumed bool   `json:"resumed"`
	Message string `json:"message"`
}

// DiscDetectRequest triggers disc detection using the configured device.
type DiscDetectRequest struct{}

// DiscDetectResponse reports the result of disc detection.
type DiscDetectResponse struct {
	Handled bool   `json:"handled"`
	Message string `json:"message"`
	ItemID  int64  `json:"item_id,omitempty"`
}
