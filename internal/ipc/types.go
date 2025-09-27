package ipc

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

// QueueItem summarizes a queue entry for CLI presentation.
type QueueItem struct {
	ID              int64   `json:"id"`
	DiscTitle       string  `json:"disc_title"`
	SourcePath      string  `json:"source_path"`
	Status          string  `json:"status"`
	ProgressStage   string  `json:"progress_stage"`
	ProgressPercent float64 `json:"progress_percent"`
	ProgressMessage string  `json:"progress_message"`
	ErrorMessage    string  `json:"error_message"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	DiscFingerprint string  `json:"disc_fingerprint"`
}

// StageHealth describes readiness of a workflow stage.
type StageHealth struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail"`
}

// StatusResponse represents combined daemon/workflow status information.
type StatusResponse struct {
	Running     bool           `json:"running"`
	QueueStats  map[string]int `json:"queue_stats"`
	LastError   string         `json:"last_error"`
	LastItem    *QueueItem     `json:"last_item"`
	LockPath    string         `json:"lock_path"`
	QueueDBPath string         `json:"queue_db_path"`
	StageHealth []StageHealth  `json:"stage_health"`
}

// QueueListRequest filters queue listing by status.
type QueueListRequest struct {
	Statuses []string `json:"statuses"`
}

// QueueListResponse contains queue entries.
type QueueListResponse struct {
	Items []QueueItem `json:"items"`
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

// AddFileRequest enqueues a manual file for processing.
type AddFileRequest struct {
	Path string `json:"path"`
}

// AddFileResponse returns the created queue item.
type AddFileResponse struct {
	Item QueueItem `json:"item"`
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

// QueueHealthRequest fetches aggregate diagnostics.
type QueueHealthRequest struct{}

// QueueHealthResponse reports queue health information.
type QueueHealthResponse struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Failed     int `json:"failed"`
	Review     int `json:"review"`
	Completed  int `json:"completed"`
}
