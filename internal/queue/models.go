package queue

import "time"

// Status represents the lifecycle of a queue item.
type Status string

const (
	StatusPending     Status = "pending"
	StatusIdentifying Status = "identifying"
	StatusIdentified  Status = "identified"
	StatusRipping     Status = "ripping"
	StatusRipped      Status = "ripped"
	StatusEncoding    Status = "encoding"
	StatusEncoded     Status = "encoded"
	StatusOrganizing  Status = "organizing"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusReview      Status = "review"
)

var processingStatuses = map[Status]struct{}{
	StatusIdentifying: {},
	StatusRipping:     {},
	StatusEncoding:    {},
	StatusOrganizing:  {},
}

// DatabaseHealth captures diagnostic information about the queue database.
type DatabaseHealth struct {
	DBPath           string
	DatabaseExists   bool
	DatabaseReadable bool
	SchemaVersion    string
	TableExists      bool
	ColumnsPresent   []string
	MissingColumns   []string
	IntegrityCheck   bool
	TotalItems       int
	Error            string
}

// HealthSummary describes aggregated queue counts per key lifecycle states.
type HealthSummary struct {
	Total      int
	Pending    int
	Processing int
	Failed     int
	Review     int
	Completed  int
}

// Item represents a queue item persisted in SQLite.
type Item struct {
	ID              int64
	SourcePath      string
	DiscTitle       string
	Status          Status
	MediaInfoJSON   string
	RippedFile      string
	EncodedFile     string
	FinalFile       string
	ErrorMessage    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ProgressStage   string
	ProgressPercent float64
	ProgressMessage string
	RipSpecData     string
	DiscFingerprint string
	MetadataJSON    string
	LastHeartbeat   *time.Time
	NeedsReview     bool
	ReviewReason    string
}

// IsProcessing returns true when the status reflects an in-flight operation.
func (i Item) IsProcessing() bool {
	_, ok := processingStatuses[i.Status]
	return ok
}
