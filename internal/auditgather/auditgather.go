package auditgather

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

// Report is the top-level audit report.
type Report struct {
	Item            ItemSummary      `json:"item"`
	FurthestStage   string           `json:"furthest_stage"`
	MediaType       string           `json:"media_type"`
	DiscSource      string           `json:"disc_source,omitempty"`
	Edition         string           `json:"edition,omitempty"`
	PhaseLogs       bool             `json:"phase_logs"`
	PhaseRipCache   bool             `json:"phase_rip_cache"`
	PhaseEpisodeID  bool             `json:"phase_episode_id"`
	PhaseEncoded    bool             `json:"phase_encoded"`
	PhaseCrop       bool             `json:"phase_crop"`
	PhaseEdition    bool             `json:"phase_edition"`
	PhaseSubtitles  bool             `json:"phase_subtitles"`
	PhaseCommentary bool             `json:"phase_commentary"`
	PhaseExtVal     bool             `json:"phase_external_validation"`
	Logs            *LogAnalysis     `json:"logs,omitempty"`
	RipCache        *RipCacheReport  `json:"rip_cache,omitempty"`
	Envelope        *EnvelopeReport  `json:"envelope,omitempty"`
	Encoding        *EncodingReport  `json:"encoding,omitempty"`
	Media           []MediaFileProbe `json:"media,omitempty"`
	MediaOmitted    int              `json:"media_omitted,omitempty"`
	Analysis        *Analysis        `json:"analysis,omitempty"`
	Errors          []string         `json:"errors,omitempty"`
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
}

// LogAnalysis holds log entries filtered by item ID.
type LogAnalysis struct {
	Path       string        `json:"path"`
	TotalLines int           `json:"total_lines"`
	Decisions  []LogDecision `json:"decisions"`
	Warnings   []LogEntry    `json:"warnings"`
	Errors     []LogEntry    `json:"errors"`
	Stages     []StageEvent  `json:"stages"`
}

// LogDecision captures a single decision from the log.
type LogDecision struct {
	TS             string `json:"ts"`
	DecisionType   string `json:"decision_type"`
	DecisionResult string `json:"decision_result"`
	DecisionReason string `json:"decision_reason"`
	Message        string `json:"message"`
}

// LogEntry captures a warning or error log entry.
type LogEntry struct {
	TS        string            `json:"ts"`
	Level     string            `json:"level"`
	Message   string            `json:"message"`
	EventType string            `json:"event_type,omitempty"`
	ErrorHint string            `json:"error_hint,omitempty"`
	Extras    map[string]string `json:"extras,omitempty"`
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
	Fingerprint string                    `json:"fingerprint"`
	ContentKey  string                    `json:"content_key"`
	Metadata    ripspec.Metadata          `json:"metadata"`
	Titles      []ripspec.Title           `json:"titles"`
	Episodes    []ripspec.Episode         `json:"episodes"`
	Assets      ripspec.Assets            `json:"assets"`
	Attributes  ripspec.EnvelopeAttributes `json:"attributes"`
}

// EncodingReport holds the encoding state snapshot.
type EncodingReport struct {
	Snapshot encodingstate.Snapshot `json:"snapshot"`
}

// MediaFileProbe holds FFprobe results for a single media file.
type MediaFileProbe struct {
	Path            string         `json:"path"`
	Role            string         `json:"role"`
	EpisodeKey      string         `json:"episode_key,omitempty"`
	Representative  bool           `json:"representative,omitempty"`
	Probe           *ffprobe.Result `json:"probe,omitempty"`
	SizeBytes       int64          `json:"size_bytes"`
	DurationSeconds float64        `json:"duration_seconds"`
	Error           string         `json:"error,omitempty"`
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

// DecisionGroup aggregates identical decisions with a count.
type DecisionGroup struct {
	DecisionType   string        `json:"decision_type"`
	DecisionResult string        `json:"decision_result"`
	DecisionReason string        `json:"decision_reason"`
	Count          int           `json:"count"`
	Entries        []LogDecision `json:"entries,omitempty"`
}

// EpisodeConsistency holds majority media profile and per-episode deviations.
type EpisodeConsistency struct {
	MajorityProfile MediaProfile         `json:"majority_profile"`
	MajorityCount   int                  `json:"majority_count"`
	TotalEpisodes   int                  `json:"total_episodes"`
	Deviations      []EpisodeDeviation   `json:"deviations,omitempty"`
}

// MediaProfile describes a media file's stream characteristics.
type MediaProfile struct {
	VideoCodec      string `json:"video_codec"`
	Resolution      string `json:"resolution"`
	AudioStreams    int    `json:"audio_streams"`
	SubtitleStreams int    `json:"subtitle_streams"`
}

// EpisodeDeviation records how an episode differs from the majority profile.
type EpisodeDeviation struct {
	EpisodeKey string       `json:"episode_key"`
	Profile    MediaProfile `json:"profile"`
	Diffs      []string     `json:"diffs"`
}

// CropAnalysis holds crop detection results.
type CropAnalysis struct {
	Filter        string  `json:"filter"`
	OutputWidth   int     `json:"output_width"`
	OutputHeight  int     `json:"output_height"`
	AspectRatio   float64 `json:"aspect_ratio"`
	StandardRatio string  `json:"standard_ratio"`
}

// EpisodeStats holds episode identification summary.
type EpisodeStats struct {
	Count              int     `json:"count"`
	Matched            int     `json:"matched"`
	Unresolved         int     `json:"unresolved"`
	ConfidenceMin      float64 `json:"confidence_min"`
	ConfidenceMax      float64 `json:"confidence_max"`
	ConfidenceMean     float64 `json:"confidence_mean"`
	SequenceContiguous bool    `json:"sequence_contiguous"`
	EpisodeRange       string  `json:"episode_range"`
}

// MediaStats holds duration and size ranges across all files.
type MediaStats struct {
	FileCount      int     `json:"file_count"`
	DurationMinSec float64 `json:"duration_min_sec"`
	DurationMaxSec float64 `json:"duration_max_sec"`
	SizeMinBytes   int64   `json:"size_min_bytes"`
	SizeMaxBytes   int64   `json:"size_max_bytes"`
}

// AssetHealth holds per-stage asset counts.
type AssetHealth struct {
	Ripped    AssetCounts `json:"ripped"`
	Encoded   AssetCounts `json:"encoded"`
	Subtitled AssetCounts `json:"subtitled"`
	Final     AssetCounts `json:"final"`
}

// AssetCounts holds counts for a single asset stage.
type AssetCounts struct {
	Total  int `json:"total"`
	OK     int `json:"ok"`
	Failed int `json:"failed"`
	Muxed  int `json:"muxed"`
}

// Anomaly is an auto-detected red flag.
type Anomaly struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// stageOrder maps stages to numeric order for furthest-stage computation.
var stageOrder = map[queue.Stage]int{
	queue.StagePending:                0,
	queue.StageIdentification:         1,
	queue.StageRipping:                2,
	queue.StageEpisodeIdentification:  3,
	queue.StageEncoding:               4,
	queue.StageAudioAnalysis:          5,
	queue.StageSubtitling:             6,
	queue.StageOrganizing:             7,
	queue.StageCompleted:              8,
}

// Gather collects all audit artifacts for a queue item.
func Gather(_ context.Context, cfg *config.Config, item *queue.Item) (*Report, error) {
	if item == nil {
		return nil, fmt.Errorf("nil queue item")
	}

	report := &Report{
		Item: ItemSummary{
			ID:              item.ID,
			DiscTitle:       item.DiscTitle,
			Stage:           string(item.Stage),
			FailedAtStage:   item.FailedAtStage,
			ErrorMessage:    item.ErrorMessage,
			NeedsReview:     item.NeedsReview != 0,
			ReviewReason:    item.ReviewReason,
			DiscFingerprint: item.DiscFingerprint,
			CreatedAt:       item.CreatedAt,
			UpdatedAt:       item.UpdatedAt,
			ProgressStage:   item.ProgressStage,
			ProgressPercent: item.ProgressPercent,
			ProgressMessage: item.ProgressMessage,
		},
	}

	// Determine furthest stage reached.
	furthest := item.Stage
	if item.Stage == queue.StageFailed && item.FailedAtStage != "" {
		furthest = queue.Stage(item.FailedAtStage)
	}
	report.FurthestStage = string(furthest)

	// Parse envelope to determine media type and disc source.
	if item.RipSpecData != "" {
		var env ripspec.Envelope
		if err := json.Unmarshal([]byte(item.RipSpecData), &env); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("parse envelope: %v", err))
		} else {
			report.MediaType = env.Metadata.MediaType
			report.DiscSource = env.Metadata.DiscSource
			report.Edition = env.Metadata.Edition
			report.Envelope = &EnvelopeReport{
				Fingerprint: env.Fingerprint,
				ContentKey:  env.ContentKey,
				Metadata:    env.Metadata,
				Titles:      env.Titles,
				Episodes:    env.Episodes,
				Assets:      env.Assets,
				Attributes:  env.Attributes,
			}
		}
	}

	// Compute phase applicability flags.
	order := stageOrder[furthest]
	report.PhaseLogs = true
	report.PhaseRipCache = order >= stageOrder[queue.StageRipping]
	report.PhaseEpisodeID = report.MediaType == "tv" && order >= stageOrder[queue.StageEpisodeIdentification]
	report.PhaseEncoded = order >= stageOrder[queue.StageEncoding]
	report.PhaseCrop = order >= stageOrder[queue.StageEncoding]
	report.PhaseEdition = report.MediaType == "movie" && order >= stageOrder[queue.StageIdentification]
	report.PhaseSubtitles = order >= stageOrder[queue.StageSubtitling]
	report.PhaseCommentary = order >= stageOrder[queue.StageAudioAnalysis]
	report.PhaseExtVal = order >= stageOrder[queue.StageEncoding] && report.DiscSource != "dvd"

	// Parse encoding snapshot if available.
	if item.EncodingDetailsJSON != "" {
		var snap encodingstate.Snapshot
		if err := json.Unmarshal([]byte(item.EncodingDetailsJSON), &snap); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("parse encoding snapshot: %v", err))
		} else {
			report.Encoding = &EncodingReport{Snapshot: snap}
		}
	}

	// Suppress unused import warnings -- cfg is used in later phases
	// (log analysis, rip cache lookup, media probes) not yet implemented.
	_ = cfg

	return report, nil
}
