package auditgather

import (
	"spindle/internal/encodingstate"
	"spindle/internal/media/ffprobe"
	"spindle/internal/ripcache"
	"spindle/internal/ripspec"
)

// Report is the structured output of the audit-gather command.
// It contains all artifacts needed for the itemaudit skill to
// analyze a queue item without performing its own file discovery
// or tool invocation.
type Report struct {
	Item      ItemSummary      `json:"item"`
	StageGate StageGate        `json:"stage_gate"`
	Logs      *LogAnalysis     `json:"logs,omitempty"`
	RipCache  *RipCacheReport  `json:"rip_cache,omitempty"`
	Envelope  *EnvelopeReport  `json:"envelope,omitempty"`
	Encoding  *EncodingReport  `json:"encoding,omitempty"`
	Media     []MediaFileProbe `json:"media,omitempty"`
	Errors    []string         `json:"errors,omitempty"`
}

// ItemSummary captures key queue item fields.
type ItemSummary struct {
	ID              int64  `json:"id"`
	DiscTitle       string `json:"disc_title"`
	Status          string `json:"status"`
	FailedAtStatus  string `json:"failed_at_status,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
	NeedsReview     bool   `json:"needs_review"`
	ReviewReason    string `json:"review_reason,omitempty"`
	DiscFingerprint string `json:"disc_fingerprint,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	ProgressStage   string `json:"progress_stage,omitempty"`
	ProgressPercent float64 `json:"progress_percent,omitempty"`
	ProgressMessage string `json:"progress_message,omitempty"`
	ItemLogPath     string `json:"item_log_path,omitempty"`
	RippedFile      string `json:"ripped_file,omitempty"`
	EncodedFile     string `json:"encoded_file,omitempty"`
	FinalFile       string `json:"final_file,omitempty"`
}

// StageGate determines which audit phases are applicable.
type StageGate struct {
	FurthestStage string `json:"furthest_stage"`
	MediaType     string `json:"media_type"`
	DiscSource    string `json:"disc_source"`
	Edition       string `json:"edition,omitempty"`

	// Which phases apply to this item.
	PhaseLogs              bool `json:"phase_logs"`
	PhaseRipCache          bool `json:"phase_rip_cache"`
	PhaseEpisodeID         bool `json:"phase_episode_id"`
	PhaseEncoded           bool `json:"phase_encoded"`
	PhaseCrop              bool `json:"phase_crop"`
	PhaseEdition           bool `json:"phase_edition"`
	PhaseSubtitles         bool `json:"phase_subtitles"`
	PhaseCommentary        bool `json:"phase_commentary"`
	PhaseExternalValidation bool `json:"phase_external_validation"`
}

// LogAnalysis captures parsed log data.
type LogAnalysis struct {
	Path               string `json:"path"`
	IsDebug            bool   `json:"is_debug"`
	TotalLines         int    `json:"total_lines"`
	InferredDiscSource string `json:"inferred_disc_source,omitempty"`

	Decisions []LogDecision `json:"decisions,omitempty"`
	Warnings  []LogEntry    `json:"warnings,omitempty"`
	Errors    []LogEntry    `json:"errors,omitempty"`
	Stages    []StageEvent  `json:"stages,omitempty"`
}

// LogDecision captures a structured decision log entry.
type LogDecision struct {
	Timestamp      string `json:"ts"`
	DecisionType   string `json:"decision_type"`
	DecisionResult string `json:"decision_result"`
	DecisionReason string `json:"decision_reason,omitempty"`
	Message        string `json:"message"`
	RawJSON        string `json:"raw_json"`
}

// LogEntry captures a warning or error log entry.
type LogEntry struct {
	Timestamp string `json:"ts"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	EventType string `json:"event_type,omitempty"`
	ErrorHint string `json:"error_hint,omitempty"`
	RawJSON   string `json:"raw_json"`
}

// StageEvent captures stage start/complete events for timing analysis.
type StageEvent struct {
	Timestamp string  `json:"ts"`
	EventType string  `json:"event_type"`
	Stage     string  `json:"stage"`
	Message   string  `json:"message"`
	Duration  float64 `json:"duration_seconds,omitempty"`
	RawJSON   string  `json:"raw_json"`
}

// RipCacheReport captures the rip cache metadata for a queue item.
type RipCacheReport struct {
	Path     string                `json:"path"`
	Found    bool                  `json:"found"`
	Metadata *ripcache.EntryMetadata `json:"metadata,omitempty"`
}

// EnvelopeReport surfaces the parsed ripspec Envelope in a structured way.
type EnvelopeReport struct {
	Fingerprint string         `json:"fingerprint,omitempty"`
	ContentKey  string         `json:"content_key,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Titles      []ripspec.Title   `json:"titles,omitempty"`
	Episodes    []ripspec.Episode `json:"episodes,omitempty"`
	Assets      ripspec.Assets    `json:"assets"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// EncodingReport surfaces the encoding details snapshot.
type EncodingReport struct {
	Snapshot encodingstate.Snapshot `json:"snapshot"`
}

// MediaFileProbe captures ffprobe output for a single media file.
type MediaFileProbe struct {
	Path        string        `json:"path"`
	Role        string        `json:"role"`
	EpisodeKey  string        `json:"episode_key,omitempty"`
	Probe       ffprobe.Result `json:"probe"`
	SizeBytes   int64         `json:"size_bytes"`
	DurationSec float64       `json:"duration_seconds"`
	Error       string        `json:"error,omitempty"`
}
