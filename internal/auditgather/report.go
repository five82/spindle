package auditgather

import (
	"time"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

// Report is the top-level audit report produced by Gather.
type Report struct {
	Item         ItemSummary       `json:"item"`
	StageGate    StageGate         `json:"stage_gate"`
	Paths        AuditPaths        `json:"paths"`
	Logs         *LogAnalysis      `json:"logs,omitempty"`
	RipCache     *RipCacheReport   `json:"rip_cache,omitempty"`
	Envelope     *ripspec.Envelope `json:"envelope,omitempty"`
	Encoding     *EncodingReport   `json:"encoding,omitempty"`
	Media        []MediaFileProbe  `json:"media,omitempty"`
	MediaOmitted int               `json:"media_omitted,omitempty"`
	Analysis     *Analysis         `json:"analysis,omitempty"`
	Errors       []string          `json:"errors,omitempty"`
}

// AuditPaths exposes configured output roots used by routing validation.
type AuditPaths struct {
	ReviewDir  string `json:"review_dir,omitempty"`
	LibraryDir string `json:"library_dir,omitempty"`
}

// ItemSummary holds queue item identification and status fields. Per-task
// progress lives in Tasks (sourced from the scheduler's tasks table); the
// item's Stage is the scheduler's coarse position and lags running tasks
// during overlap windows.
type ItemSummary struct {
	ID              int64         `json:"id"`
	DiscTitle       string        `json:"disc_title"`
	Stage           string        `json:"stage"`
	FailedAtStage   string        `json:"failed_at_stage,omitempty"`
	ErrorMessage    string        `json:"error_message,omitempty"`
	NeedsReview     bool          `json:"needs_review"`
	ReviewReasons   []string      `json:"review_reasons,omitempty"`
	DiscFingerprint string        `json:"disc_fingerprint,omitempty"`
	CreatedAt       string        `json:"created_at"`
	UpdatedAt       string        `json:"updated_at"`
	Tasks           []TaskSummary `json:"tasks,omitempty"`
	RippedFile      string        `json:"ripped_file,omitempty"`
	EncodedFile     string        `json:"encoded_file,omitempty"`
	FinalFile       string        `json:"final_file,omitempty"`
}

// TaskSummary is a compact per-task status entry sourced from the item's
// task rows. Progress fields are populated only while the task is running or
// once it has run; pending tasks show zero values.
type TaskSummary struct {
	Type            string  `json:"type"`
	State           string  `json:"state"`
	Attempts        int     `json:"attempts,omitempty"`
	Error           string  `json:"error,omitempty"`
	ProgressPercent float64 `json:"progress_percent,omitempty"`
	ProgressMessage string  `json:"progress_message,omitempty"`
	ActiveAssetKey  string  `json:"active_asset_key,omitempty"`
}

// StageGate determines which audit phases are applicable.
type StageGate struct {
	FurthestStage   string `json:"furthest_stage"`
	MediaType       string `json:"media_type"`
	MediaHint       string `json:"media_hint,omitempty"`
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

// LogAnalysis holds log entries filtered by item ID across every daemon log
// file overlapping the item's lifetime. LinesScanned counts all lines read,
// not just the item's. EventsOmitted counts progress events dropped by
// downsampling.
type LogAnalysis struct {
	Paths              []string      `json:"paths"`
	IsDebug            bool          `json:"is_debug"`
	LinesScanned       int           `json:"lines_scanned"`
	InferredDiscSource string        `json:"inferred_disc_source,omitempty"`
	InferredMediaHint  string        `json:"inferred_media_hint,omitempty"`
	Decisions          []LogDecision `json:"decisions,omitempty"`
	Warnings           []LogEntry    `json:"warnings,omitempty"`
	Errors             []LogEntry    `json:"errors,omitempty"`
	Events             []LogEntry    `json:"events,omitempty"`
	EventsOmitted      int           `json:"events_omitted,omitempty"`
	Stages             []StageEvent  `json:"stages,omitempty"`
}

// LogDecision captures a single decision from the log.
type LogDecision struct {
	TS             string         `json:"ts"`
	DecisionType   string         `json:"decision_type"`
	DecisionResult string         `json:"decision_result"`
	DecisionReason string         `json:"decision_reason,omitempty"`
	Message        string         `json:"message"`
	Extras         map[string]any `json:"extras,omitempty"`
}

// LogEntry captures a warning, error, or item-specific informational event.
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

// RipCacheReport holds rip cache lookup results. Disabled distinguishes a
// cache that is turned off in config from one whose entry was pruned.
type RipCacheReport struct {
	Path     string            `json:"path,omitempty"`
	Found    bool              `json:"found"`
	Disabled bool              `json:"disabled,omitempty"`
	Metadata *ripCacheMetadata `json:"metadata,omitempty"`
}

// ripCacheMetadata is cache metadata safe for compact audit output. Large
// serialized blobs are omitted because the parsed envelope already carries the
// useful contents.
type ripCacheMetadata struct {
	Version     int       `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	DiscTitle   string    `json:"disc_title"`
	CachedAt    time.Time `json:"cached_at"`
	TitleCount  int       `json:"title_count"`
	TotalBytes  int64     `json:"total_bytes"`
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
	DecisionGroups     []DecisionGroup        `json:"decision_groups,omitempty"`
	NotableDecisions   []LogDecision          `json:"notable_decisions,omitempty"`
	StageTimings       []StageTiming          `json:"stage_timings,omitempty"`
	SourceSummary      *SourceSummary         `json:"source_summary,omitempty"`
	TitleSelection     *TitleSelectionSummary `json:"title_selection,omitempty"`
	OutputMedia        []MediaSummary         `json:"output_media,omitempty"`
	AudioSummary       *AudioSummary          `json:"audio_summary,omitempty"`
	SubtitleSummary    *SubtitleSummary       `json:"subtitle_summary,omitempty"`
	RoutingSummary     *RoutingSummary        `json:"routing_summary,omitempty"`
	EpisodeConsistency *EpisodeConsistency    `json:"episode_consistency,omitempty"`
	CropAnalysis       *CropAnalysis          `json:"crop_analysis,omitempty"`
	EpisodeStats       *EpisodeStats          `json:"episode_stats,omitempty"`
	MediaStats         *MediaStats            `json:"media_stats,omitempty"`
	AssetHealth        *AssetHealth           `json:"asset_health,omitempty"`
	Anomalies          []Anomaly              `json:"anomalies,omitempty"`
}

// StageTiming is a compact per-stage timing summary.
type StageTiming struct {
	Stage           string  `json:"stage"`
	StartedAt       string  `json:"started_at,omitempty"`
	CompletedAt     string  `json:"completed_at,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	Starts          int     `json:"starts,omitempty"`
	Completions     int     `json:"completions,omitempty"`
}

// SourceSummary captures deterministic source/output traits used for external validation.
type SourceSummary struct {
	DiscSource       string   `json:"disc_source,omitempty"`
	UHDLikely        bool     `json:"uhd_likely,omitempty"`
	InputResolution  string   `json:"input_resolution,omitempty"`
	OutputResolution string   `json:"output_resolution,omitempty"`
	InputCodecs      []string `json:"input_codecs,omitempty"`
	OutputCodec      string   `json:"output_codec,omitempty"`
	DynamicRange     string   `json:"dynamic_range,omitempty"`
	HDR              bool     `json:"hdr,omitempty"`
}

// TitleSelectionSummary describes feature-length title candidates and selection.
type TitleSelectionSummary struct {
	SelectedID              int              `json:"selected_id"`
	SelectedDurationSeconds int              `json:"selected_duration_seconds,omitempty"`
	DecisionResult          string           `json:"decision_result,omitempty"`
	DecisionReason          string           `json:"decision_reason,omitempty"`
	FeatureCandidateCount   int              `json:"feature_candidate_count"`
	SimilarRuntimeCount     int              `json:"similar_runtime_count,omitempty"`
	Candidates              []TitleCandidate `json:"candidates,omitempty"`
}

// TitleCandidate is a compact MakeMKV title summary.
type TitleCandidate struct {
	ID              int    `json:"id"`
	DurationSeconds int    `json:"duration_seconds"`
	Chapters        int    `json:"chapters"`
	Playlist        string `json:"playlist,omitempty"`
	SegmentCount    int    `json:"segment_count,omitempty"`
	Selected        bool   `json:"selected,omitempty"`
}

// MediaSummary is a compact stream summary for an output media file.
type MediaSummary struct {
	Path            string                  `json:"path"`
	Role            string                  `json:"role,omitempty"`
	EpisodeKey      string                  `json:"episode_key,omitempty"`
	DurationSeconds float64                 `json:"duration_seconds,omitempty"`
	SizeBytes       int64                   `json:"size_bytes,omitempty"`
	Video           *VideoSummary           `json:"video,omitempty"`
	Audio           []AudioStreamSummary    `json:"audio,omitempty"`
	Subtitles       []SubtitleStreamSummary `json:"subtitles,omitempty"`
}

// VideoSummary describes the primary video stream.
type VideoSummary struct {
	Codec          string `json:"codec,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	HDR            bool   `json:"hdr,omitempty"`
	ColorTransfer  string `json:"color_transfer,omitempty"`
	ColorPrimaries string `json:"color_primaries,omitempty"`
}

// AudioStreamSummary describes a single output audio stream.
type AudioStreamSummary struct {
	Index        int    `json:"index"`
	Codec        string `json:"codec,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	Layout       string `json:"layout,omitempty"`
	Language     string `json:"language,omitempty"`
	Title        string `json:"title,omitempty"`
	Default      bool   `json:"default,omitempty"`
	Commentary   bool   `json:"commentary,omitempty"`
	LabelCorrect bool   `json:"label_correct"`
}

// SubtitleStreamSummary describes a single output subtitle stream.
type SubtitleStreamSummary struct {
	Index        int    `json:"index"`
	Codec        string `json:"codec,omitempty"`
	Language     string `json:"language,omitempty"`
	Title        string `json:"title,omitempty"`
	Default      bool   `json:"default,omitempty"`
	Forced       bool   `json:"forced,omitempty"`
	LabelCorrect bool   `json:"label_correct"`
}

// AudioSummary condenses audio selection/refinement and commentary evidence.
type AudioSummary struct {
	PrimaryDescription      string          `json:"primary_description,omitempty"`
	PrimaryTrackIndex       int             `json:"primary_track_index"`
	OutputAudioTracks       int             `json:"output_audio_tracks,omitempty"`
	OutputCommentaryTracks  int             `json:"output_commentary_tracks,omitempty"`
	ExcludedTracks          []ExcludedTrack `json:"excluded_tracks,omitempty"`
	CommentaryDecisions     []LogDecision   `json:"commentary_decisions,omitempty"`
	CommentaryLabelsCorrect bool            `json:"commentary_labels_correct"`
}

// ExcludedTrack summarizes an audio track intentionally removed during refinement.
type ExcludedTrack struct {
	Index      int     `json:"index"`
	Reason     string  `json:"reason,omitempty"`
	Similarity float64 `json:"similarity,omitempty"`
}

// SubtitleSummary condenses subtitle generation and muxing outcome.
type SubtitleSummary struct {
	Results               []SubtitleResultSummary `json:"results,omitempty"`
	ValidationPassed      int                     `json:"validation_passed,omitempty"`
	ValidationNeedsReview int                     `json:"validation_needs_review,omitempty"`
	ValidationFailed      int                     `json:"validation_failed,omitempty"`
	OutputSubtitleTracks  int                     `json:"output_subtitle_tracks,omitempty"`
	SubtitleLabelsCorrect bool                    `json:"subtitle_labels_correct"`
}

// SubtitleResultSummary is the actionable part of one subtitle-generation record.
type SubtitleResultSummary struct {
	EpisodeKey        string   `json:"episode_key,omitempty"`
	Source            string   `json:"source,omitempty"`
	Language          string   `json:"language,omitempty"`
	Segments          int      `json:"segments,omitempty"`
	ValidationResult  string   `json:"validation_result,omitempty"`
	ReviewIssues      []string `json:"review_issues,omitempty"`
	SevereIssues      []string `json:"severe_issues,omitempty"`
	QCObservations    []string `json:"qc_observations,omitempty"`
	AuditResult       string   `json:"audit_result,omitempty"`
	AuditEditsApplied int      `json:"audit_edits_applied,omitempty"`
	AuditEditsDropped int      `json:"audit_edits_dropped,omitempty"`
}

// RoutingSummary classifies final outputs against configured library/review roots.
type RoutingSummary struct {
	Entries []RoutingEntry `json:"entries,omitempty"`
}

// RoutingEntry describes one final output route.
type RoutingEntry struct {
	EpisodeKey      string `json:"episode_key,omitempty"`
	Path            string `json:"path"`
	Destination     string `json:"destination"`
	ExpectedReview  bool   `json:"expected_review,omitempty"`
	MatchesExpected bool   `json:"matches_expected"`
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
	PlaceholderOnly    bool    `json:"placeholder_only,omitempty"`
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
	Ripped     *AssetCounts `json:"ripped,omitempty"`
	Encoded    *AssetCounts `json:"encoded,omitempty"`
	Subtitled  *AssetCounts `json:"subtitled,omitempty"`
	Final      *AssetCounts `json:"final,omitempty"`
	Transcript *AssetCounts `json:"transcript,omitempty"`
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
