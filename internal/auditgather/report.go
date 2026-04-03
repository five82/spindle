package auditgather

import (
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

// Report is the top-level audit report produced by Gather.
type Report struct {
	Item         ItemSummary      `json:"item"`
	StageGate    StageGate        `json:"stage_gate"`
	Paths        AuditPaths       `json:"paths"`
	Logs         *LogAnalysis     `json:"logs,omitempty"`
	RipCache     *RipCacheReport  `json:"rip_cache,omitempty"`
	Envelope     *EnvelopeReport  `json:"envelope,omitempty"`
	Encoding     *EncodingReport  `json:"encoding,omitempty"`
	Media        []MediaFileProbe `json:"media,omitempty"`
	MediaOmitted int              `json:"media_omitted,omitempty"`
	Analysis     *Analysis        `json:"analysis,omitempty"`
	Errors       []string         `json:"errors,omitempty"`
}

// AuditPaths exposes configured output roots used by routing validation.
type AuditPaths struct {
	ReviewDir  string `json:"review_dir,omitempty"`
	LibraryDir string `json:"library_dir,omitempty"`
}

// ItemSummary holds queue item identification and status fields.
type ItemSummary struct {
	ID              int64   `json:"id"`
	DiscTitle       string  `json:"disc_title"`
	Stage           string  `json:"stage"`
	FailedAtStage   string  `json:"failed_at_stage,omitempty"`
	ErrorMessage    string  `json:"error_message,omitempty"`
	NeedsReview     bool    `json:"needs_review"`
	ReviewReason    string  `json:"review_reason,omitempty"`
	DiscFingerprint string  `json:"disc_fingerprint,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	ProgressStage   string  `json:"progress_stage,omitempty"`
	ProgressPercent float64 `json:"progress_percent,omitempty"`
	ProgressMessage string  `json:"progress_message,omitempty"`
	RippedFile      string  `json:"ripped_file,omitempty"`
	EncodedFile     string  `json:"encoded_file,omitempty"`
	FinalFile       string  `json:"final_file,omitempty"`
}

// StageGate determines which audit phases are applicable.
type StageGate struct {
	FurthestStage   string `json:"furthest_stage"`
	MediaType       string `json:"media_type"`
	DiscSource      string `json:"disc_source"`
	PhaseLogs       bool   `json:"phase_logs"`
	PhaseRipCache   bool   `json:"phase_rip_cache"`
	PhaseEpisodeID  bool   `json:"phase_episode_id"`
	PhaseEncoded    bool   `json:"phase_encoded"`
	PhaseCrop       bool   `json:"phase_crop"`
	PhaseSubtitles  bool   `json:"phase_subtitles"`
	PhaseCommentary bool   `json:"phase_commentary"`
	PhaseExtVal     bool   `json:"phase_external_validation"`
}

// LogAnalysis holds log entries filtered by item ID.
type LogAnalysis struct {
	Path               string        `json:"path"`
	IsDebug            bool          `json:"is_debug"`
	TotalLines         int           `json:"total_lines"`
	InferredDiscSource string        `json:"inferred_disc_source,omitempty"`
	Decisions          []LogDecision `json:"decisions,omitempty"`
	Warnings           []LogEntry    `json:"warnings,omitempty"`
	Errors             []LogEntry    `json:"errors,omitempty"`
	Stages             []StageEvent  `json:"stages,omitempty"`
}

// LogDecision captures a single decision from the log.
type LogDecision struct {
	TS             string `json:"ts"`
	DecisionType   string `json:"decision_type"`
	DecisionResult string `json:"decision_result"`
	DecisionReason string `json:"decision_reason,omitempty"`
	Message        string `json:"message"`
}

// LogEntry captures a warning or error log entry.
type LogEntry struct {
	TS        string         `json:"ts"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	EventType string         `json:"event_type,omitempty"`
	ErrorHint string         `json:"error_hint,omitempty"`
	Extras    map[string]any `json:"extras,omitempty"`
}

// StageEvent captures a stage transition event from the log.
type StageEvent struct {
	TS              string  `json:"ts"`
	EventType       string  `json:"event_type"`
	Stage           string  `json:"stage"`
	Message         string  `json:"message"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

// RipCacheReport holds rip cache lookup results.
type RipCacheReport struct {
	Path     string                  `json:"path"`
	Found    bool                    `json:"found"`
	Metadata *ripcache.EntryMetadata `json:"metadata,omitempty"`
}

// EnvelopeReport holds the parsed RipSpec envelope.
type EnvelopeReport struct {
	Fingerprint string                     `json:"fingerprint,omitempty"`
	ContentKey  string                     `json:"content_key,omitempty"`
	Metadata    ripspec.Metadata           `json:"metadata,omitempty"`
	Titles      []ripspec.Title            `json:"titles,omitempty"`
	Episodes    []ripspec.Episode          `json:"episodes,omitempty"`
	Assets      ripspec.Assets             `json:"assets"`
	Attributes  ripspec.EnvelopeAttributes `json:"attributes,omitempty"`
}

// EncodingReport holds the encoding state snapshot.
type EncodingReport struct {
	Snapshot encodingstate.Snapshot `json:"snapshot"`
}

// MediaFileProbe holds FFprobe results for a single media file.
type MediaFileProbe struct {
	Path            string          `json:"path"`
	Role            string          `json:"role"`
	EpisodeKey      string          `json:"episode_key,omitempty"`
	Representative  bool            `json:"representative,omitempty"`
	Probe           *ffprobe.Result `json:"probe,omitempty"`
	SizeBytes       int64           `json:"size_bytes"`
	DurationSeconds float64         `json:"duration_seconds"`
	Error           string          `json:"error,omitempty"`
}

// Analysis holds pre-computed summaries derived from gathered data.
type Analysis struct {
	DecisionGroups     []DecisionGroup     `json:"decision_groups,omitempty"`
	EpisodeConsistency *EpisodeConsistency `json:"episode_consistency,omitempty"`
	CropAnalysis       *CropAnalysis       `json:"crop_analysis,omitempty"`
	EpisodeStats       *EpisodeStats       `json:"episode_stats,omitempty"`
	MediaStats         *MediaStats         `json:"media_stats,omitempty"`
	AssetHealth        *AssetHealth        `json:"asset_health,omitempty"`
	Anomalies          []Anomaly           `json:"anomalies,omitempty"`
}

// DecisionGroup aggregates identical decisions by (type, result, reason).
type DecisionGroup struct {
	DecisionType   string        `json:"decision_type"`
	DecisionResult string        `json:"decision_result"`
	DecisionReason string        `json:"decision_reason,omitempty"`
	Count          int           `json:"count"`
	Entries        []LogDecision `json:"entries,omitempty"`
}

// EpisodeConsistency holds majority media profile and per-episode deviations.
type EpisodeConsistency struct {
	MajorityProfile ProfileSummary     `json:"majority_profile"`
	MajorityCount   int                `json:"majority_count"`
	TotalEpisodes   int                `json:"total_episodes"`
	Deviations      []ProfileDeviation `json:"deviations,omitempty"`
}

// ProfileSummary describes a media file's stream characteristics.
type ProfileSummary struct {
	VideoCodec      string            `json:"video_codec"`
	Width           int               `json:"width"`
	Height          int               `json:"height"`
	AudioStreams    []AudioProfile    `json:"audio_streams,omitempty"`
	SubtitleStreams []SubtitleProfile `json:"subtitle_streams,omitempty"`
}

// AudioProfile describes a single audio stream.
type AudioProfile struct {
	Codec         string `json:"codec"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout,omitempty"`
	Language      string `json:"language,omitempty"`
	IsDefault     bool   `json:"is_default,omitempty"`
	IsCommentary  bool   `json:"is_commentary,omitempty"`
}

// SubtitleProfile describes a single subtitle stream.
type SubtitleProfile struct {
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	IsForced bool   `json:"is_forced,omitempty"`
}

// ProfileDeviation records how an episode differs from the majority profile.
type ProfileDeviation struct {
	EpisodeKey  string   `json:"episode_key"`
	Differences []string `json:"differences"`
}

// CropAnalysis holds crop detection results.
type CropAnalysis struct {
	Filter        string  `json:"filter,omitempty"`
	OutputWidth   int     `json:"output_width,omitempty"`
	OutputHeight  int     `json:"output_height,omitempty"`
	AspectRatio   float64 `json:"aspect_ratio,omitempty"`
	StandardRatio string  `json:"standard_ratio,omitempty"`
	Required      bool    `json:"required"`
}

// EpisodeStats holds episode identification summary.
type EpisodeStats struct {
	Count              int     `json:"count"`
	Matched            int     `json:"matched"`
	Unresolved         int     `json:"unresolved"`
	ConfidenceMin      float64 `json:"confidence_min,omitempty"`
	ConfidenceMax      float64 `json:"confidence_max,omitempty"`
	ConfidenceMean     float64 `json:"confidence_mean,omitempty"`
	Below070           int     `json:"below_070"`
	Below080           int     `json:"below_080"`
	Below090           int     `json:"below_090"`
	SequenceContiguous bool    `json:"sequence_contiguous"`
	EpisodeRange       string  `json:"episode_range,omitempty"`
}

// MediaStats holds duration and size ranges across all files.
type MediaStats struct {
	FileCount      int     `json:"file_count"`
	DurationMinSec float64 `json:"duration_min_sec,omitempty"`
	DurationMaxSec float64 `json:"duration_max_sec,omitempty"`
	SizeMinBytes   int64   `json:"size_min_bytes,omitempty"`
	SizeMaxBytes   int64   `json:"size_max_bytes,omitempty"`
}

// AssetHealth holds per-stage asset counts.
type AssetHealth struct {
	Ripped    *AssetCounts `json:"ripped,omitempty"`
	Encoded   *AssetCounts `json:"encoded,omitempty"`
	Subtitled *AssetCounts `json:"subtitled,omitempty"`
	Final     *AssetCounts `json:"final,omitempty"`
}

// AssetCounts holds counts for a single asset stage.
type AssetCounts struct {
	Total  int `json:"total"`
	OK     int `json:"ok"`
	Failed int `json:"failed"`
	Muxed  int `json:"muxed,omitempty"`
}

// Anomaly is an auto-detected red flag.
type Anomaly struct {
	Severity string `json:"severity"` // critical, warning, info
	Category string `json:"category"`
	Message  string `json:"message"`
}
