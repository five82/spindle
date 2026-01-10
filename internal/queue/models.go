package queue

import (
	"strings"
	"time"
)

// Status represents the lifecycle of a queue item.
type Status string

const (
	StatusPending            Status = "pending"
	StatusIdentifying        Status = "identifying"
	StatusIdentified         Status = "identified"
	StatusRipping            Status = "ripping"
	StatusRipped             Status = "ripped"
	StatusEpisodeIdentifying Status = "episode_identifying"
	StatusEpisodeIdentified  Status = "episode_identified"
	StatusEncoding           Status = "encoding"
	StatusEncoded            Status = "encoded"
	StatusSubtitling         Status = "subtitling"
	StatusSubtitled          Status = "subtitled"
	StatusOrganizing         Status = "organizing"
	StatusCompleted          Status = "completed"
	StatusFailed             Status = "failed"
)

// UserStopReason is the review reason set when a user explicitly stops an item.
const UserStopReason = "Stop requested by user"

// DaemonStopReason is the error message set when items are failed due to daemon shutdown.
const DaemonStopReason = "Daemon stopped"

var allStatuses = []Status{
	StatusPending,
	StatusIdentifying,
	StatusIdentified,
	StatusRipping,
	StatusRipped,
	StatusEpisodeIdentifying,
	StatusEpisodeIdentified,
	StatusEncoding,
	StatusEncoded,
	StatusSubtitling,
	StatusSubtitled,
	StatusOrganizing,
	StatusCompleted,
	StatusFailed,
}

var statusSet = func() map[Status]struct{} {
	set := make(map[Status]struct{}, len(allStatuses))
	for _, status := range allStatuses {
		set[status] = struct{}{}
	}
	return set
}()

var processingStatuses = map[Status]struct{}{
	StatusIdentifying:        {},
	StatusRipping:            {},
	StatusEpisodeIdentifying: {},
	StatusEncoding:           {},
	StatusSubtitling:         {},
	StatusOrganizing:         {},
}

type statusTransition struct {
	from Status
	to   Status
}

var stageRollbackTransitions = []statusTransition{
	{from: StatusIdentifying, to: StatusPending},
	{from: StatusRipping, to: StatusIdentified},
	{from: StatusEpisodeIdentifying, to: StatusRipped},
	{from: StatusEncoding, to: StatusEpisodeIdentified},
	{from: StatusSubtitling, to: StatusEncoded},
	{from: StatusOrganizing, to: StatusEncoded},
}

func processingRollbackTransitions() []statusTransition {
	return stageRollbackTransitions
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
	Completed  int
}

// Item represents a queue item persisted in SQLite.
type Item struct {
	ID                  int64
	SourcePath          string
	DiscTitle           string
	Status              Status
	MediaInfoJSON       string
	RippedFile          string
	EncodedFile         string
	FinalFile           string
	ItemLogPath         string
	ActiveEpisodeKey    string
	ErrorMessage        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ProgressStage       string
	ProgressPercent     float64
	ProgressMessage     string
	ProgressBytesCopied int64 // Only set during organizing
	ProgressTotalBytes  int64 // Only set during organizing
	EncodingDetailsJSON string
	DraptoPresetProfile string
	RipSpecData         string
	DiscFingerprint     string
	MetadataJSON        string
	LastHeartbeat       *time.Time
	NeedsReview         bool
	ReviewReason        string
}

// AllStatuses returns the ordered list of known statuses.
func AllStatuses() []Status {
	cp := make([]Status, len(allStatuses))
	copy(cp, allStatuses)
	return cp
}

// ParseStatus converts a string into a known Status.
func ParseStatus(value string) (Status, bool) {
	normalized := Status(strings.ToLower(strings.TrimSpace(value)))
	if normalized == "" {
		return "", false
	}
	_, ok := statusSet[normalized]
	return normalized, ok
}

// IsProcessing returns true when the status reflects an in-flight operation.
func (i Item) IsProcessing() bool {
	_, ok := processingStatuses[i.Status]
	return ok
}

// IsProcessingStatus reports whether a status reflects an in-flight operation.
func IsProcessingStatus(status Status) bool {
	_, ok := processingStatuses[status]
	return ok
}

// IsUserStopReason reports whether a review reason represents a user-initiated stop.
func IsUserStopReason(reason string) bool {
	return strings.EqualFold(strings.TrimSpace(reason), UserStopReason)
}

// InitProgress resets progress fields for a new stage.
// If ProgressStage is currently empty, it is set to the provided stage value;
// otherwise the existing stage is preserved (to support resume scenarios).
// ProgressMessage is set to message, ProgressPercent is reset to 0,
// and ErrorMessage and ActiveEpisodeKey are cleared.
func (i *Item) InitProgress(stage, message string) {
	if i.ProgressStage == "" {
		i.ProgressStage = stage
	}
	i.ProgressMessage = message
	i.ProgressPercent = 0
	i.ErrorMessage = ""
	i.ActiveEpisodeKey = ""
}

// SetProgress updates all three progress fields atomically.
// Use this instead of setting ProgressStage, ProgressPercent, and ProgressMessage individually.
func (i *Item) SetProgress(stage, message string, percent float64) {
	i.ProgressStage = stage
	i.ProgressMessage = message
	i.ProgressPercent = percent
}

// SetProgressComplete sets progress to 100% with the given stage and message.
// Convenience method for stage completion.
func (i *Item) SetProgressComplete(stage, message string) {
	i.SetProgress(stage, message, 100)
}

// SetFailed marks the item as failed with the given error message.
// Clears heartbeat and sets progress fields appropriately.
func (i *Item) SetFailed(message string) {
	i.Status = StatusFailed
	i.ErrorMessage = message
	i.ProgressPercent = 0
	i.ProgressMessage = message
	i.LastHeartbeat = nil
	i.ProgressStage = "Failed"
}

// IsInWorkflow returns true when an item is actively progressing (or queued to progress)
// through stages and should not be reset simply because the disc was reinserted.
func (i Item) IsInWorkflow() bool {
	if i.IsProcessing() {
		return true
	}
	switch i.Status {
	case StatusIdentified,
		StatusRipped,
		StatusEpisodeIdentified,
		StatusEncoded,
		StatusSubtitled,
		StatusOrganizing,
		StatusCompleted:
		return true
	default:
		return false
	}
}

// StageKey returns the normalized stage identifier used in API/CLI presentation.
func (s Status) StageKey() string {
	switch s {
	case "":
		return ""
	case StatusPending:
		return "planned"
	case StatusCompleted:
		return "final"
	case StatusIdentifying,
		StatusIdentified,
		StatusRipping,
		StatusRipped,
		StatusEpisodeIdentifying,
		StatusEpisodeIdentified,
		StatusEncoding,
		StatusEncoded,
		StatusSubtitling,
		StatusSubtitled,
		StatusOrganizing,
		StatusFailed:
		return string(s)
	default:
		return ""
	}
}

// ProcessingLane partitions workflow into user-facing foreground stages and background work.
type ProcessingLane string

const (
	LaneForeground ProcessingLane = "foreground"
	LaneBackground ProcessingLane = "background"
)

// LaneForItem maps a queue item to its processing lane for observability purposes.
func LaneForItem(item *Item) ProcessingLane {
	if item == nil {
		return LaneForeground
	}
	switch item.Status {
	case StatusPending, StatusIdentifying, StatusIdentified, StatusRipping:
		return LaneForeground
	case StatusRipped, StatusEpisodeIdentifying, StatusEpisodeIdentified, StatusEncoding, StatusEncoded, StatusOrganizing, StatusCompleted, StatusSubtitling, StatusSubtitled:
		return LaneBackground
	case StatusFailed:
		if item.ItemLogPath != "" {
			return LaneBackground
		}
		return LaneForeground
	default:
		return LaneForeground
	}
}
