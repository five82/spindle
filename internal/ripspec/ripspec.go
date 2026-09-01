package ripspec

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// CurrentVersion is the envelope schema version. Parse rejects any version
// that does not match CurrentVersion.
const CurrentVersion = 1

// Envelope is the central data structure shared across all pipeline stages.
// It is serialized as JSON in the queue rip_spec_data column.
type Envelope struct {
	Version     int                `json:"version"`
	Fingerprint string             `json:"fingerprint"`
	ContentKey  string             `json:"content_key"`
	Metadata    Metadata           `json:"metadata"`
	Titles      []Title            `json:"titles"`
	Episodes    []Episode          `json:"episodes"`
	Assets      Assets             `json:"assets"`
	Attributes  EnvelopeAttributes `json:"attributes"`
}

// Metadata holds content identification fields sourced from TMDB and disc info.
type Metadata struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Overview     string  `json:"overview,omitempty"`
	MediaType    string  `json:"media_type"`
	ShowTitle    string  `json:"show_title,omitempty"`
	SeriesTitle  string  `json:"series_title,omitempty"`
	Year         string  `json:"year,omitempty"`
	ReleaseDate  string  `json:"release_date,omitempty"`
	FirstAirDate string  `json:"first_air_date,omitempty"`
	IMDBID       string  `json:"imdb_id,omitempty"`
	Language     string  `json:"language,omitempty"`
	SeasonNumber int     `json:"season_number,omitempty"`
	DiscNumber   int     `json:"disc_number,omitempty"`
	VoteAverage  float64 `json:"vote_average,omitempty"`
	VoteCount    int     `json:"vote_count,omitempty"`
	Movie        bool    `json:"movie,omitempty"`
	Cached       bool    `json:"cached,omitempty"`
	Filename     string  `json:"filename,omitempty"`
	DiscSource   string  `json:"disc_source,omitempty"`
}

// Title represents a MakeMKV title on the disc.
type Title struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Duration       int    `json:"duration"`
	Chapters       int    `json:"chapters"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	Playlist       string `json:"playlist,omitempty"`
	SegmentCount   int    `json:"segment_count,omitempty"`
	SegmentMap     string `json:"segment_map,omitempty"`
	TitleHash      string `json:"title_hash,omitempty"`
	Season         int    `json:"season,omitempty"`
	Episode        int    `json:"episode,omitempty"`
	EpisodeTitle   string `json:"episode_title,omitempty"`
	EpisodeAirDate string `json:"episode_air_date,omitempty"`
}

// Episode tracks the mapping between a disc title and a series episode.
type Episode struct {
	Key             string  `json:"key"`
	TitleID         int     `json:"title_id"`
	Season          int     `json:"season"`
	Episode         int     `json:"episode"`
	EpisodeEnd      int     `json:"episode_end,omitempty"`
	EpisodeTitle    string  `json:"episode_title,omitempty"`
	EpisodeAirDate  string  `json:"episode_air_date,omitempty"`
	RuntimeSeconds  int     `json:"runtime_seconds,omitempty"`
	TitleHash       string  `json:"title_hash,omitempty"`
	OutputBasename  string  `json:"output_basename,omitempty"`
	MatchScore      float64 `json:"match_score,omitempty"`
	MatchConfidence float64 `json:"match_confidence,omitempty"`
	NeedsReview     bool    `json:"needs_review,omitempty"`
	ReviewReason    string  `json:"review_reason,omitempty"`
}

// Asset represents a single file artifact at a pipeline stage.
type Asset struct {
	EpisodeKey     string `json:"episode_key"`
	TitleID        int    `json:"title_id"`
	Path           string `json:"path"`
	Status         string `json:"status"`
	SubtitlesMuxed bool   `json:"subtitles_muxed,omitempty"`
	ErrorMsg       string `json:"error_msg,omitempty"`
}

// Asset status constants.
const (
	AssetStatusPending   = "pending"
	AssetStatusCompleted = "completed"
	AssetStatusFailed    = "failed"
)

// Asset kind constants identify pipeline stages.
const (
	AssetKindRipped    = "ripped"
	AssetKindEncoded   = "encoded"
	AssetKindSubtitled = "subtitled"
	AssetKindFinal     = "final"
	// AssetKindTranscript is the canonical WhisperX transcript of an
	// episode's primary audio track, transcribed from the RIPPED source.
	// Path points at the SRT; the WhisperX JSON (word timings) sits next to
	// it as audio.json in the same directory. Episode ID, commentary
	// analysis, and subtitle generation all reuse this artifact instead of
	// re-transcribing. It lives in staging and dies with staging cleanup.
	AssetKindTranscript = "transcript"
)

// Assets holds per-stage asset lists.
type Assets struct {
	Ripped     []Asset `json:"ripped,omitempty"`
	Encoded    []Asset `json:"encoded,omitempty"`
	Subtitled  []Asset `json:"subtitled,omitempty"`
	Final      []Asset `json:"final,omitempty"`
	Transcript []Asset `json:"transcript,omitempty"`
}

// AudioTrackRef identifies a primary audio track by index.
type AudioTrackRef struct {
	Index int `json:"index"`
}

// CommentaryTrackRef identifies a commentary audio track.
type CommentaryTrackRef struct {
	Index      int     `json:"index"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// ExcludedTrackRef identifies an audio track excluded from encoding.
type ExcludedTrackRef struct {
	Index      int     `json:"index"`
	Reason     string  `json:"reason"`
	Similarity float64 `json:"similarity,omitempty"`
}

// EpisodeAudioAnalysis holds commentary detection results for one episode,
// measured on the RIPPED source (track order and count are preserved by
// encoding, so the indices remain valid on the encoded file until the apply
// stage's refinement strips tracks).
type EpisodeAudioAnalysis struct {
	EpisodeKey       string               `json:"episode_key"`
	CommentaryTracks []CommentaryTrackRef `json:"commentary_tracks,omitempty"`
	ExcludedTracks   []ExcludedTrackRef   `json:"excluded_tracks,omitempty"`
}

// AudioAnalysisData holds the results of audio track analysis. The
// aggregate CommentaryTracks/ExcludedTracks lists are the union across
// episodes (single entry for movies) and back the API/audit displays;
// PerEpisode carries the per-key detail the apply stage uses.
type AudioAnalysisData struct {
	PrimaryTrack       AudioTrackRef          `json:"primary_track"`
	PrimaryDescription string                 `json:"primary_description,omitempty"`
	CommentaryTracks   []CommentaryTrackRef   `json:"commentary_tracks,omitempty"`
	ExcludedTracks     []ExcludedTrackRef     `json:"excluded_tracks,omitempty"`
	PerEpisode         []EpisodeAudioAnalysis `json:"per_episode,omitempty"`
}

// EpisodeAnalysis returns the per-episode analysis entry for key, or nil.
func (d *AudioAnalysisData) EpisodeAnalysis(key string) *EpisodeAudioAnalysis {
	if d == nil {
		return nil
	}
	lower := strings.ToLower(key)
	for i := range d.PerEpisode {
		if strings.ToLower(d.PerEpisode[i].EpisodeKey) == lower {
			return &d.PerEpisode[i]
		}
	}
	return nil
}

// SubtitleGenRecord captures the result of subtitle generation for one
// episode. Source is "opensubtitles" (adopted download) or "none" (skipped).
type SubtitleGenRecord struct {
	EpisodeKey       string   `json:"episode_key"`
	Source           string   `json:"source"`
	SubtitlePath     string   `json:"subtitle_path"`
	Segments         int      `json:"segments"`
	DurationSec      float64  `json:"duration_sec,omitempty"`
	Language         string   `json:"language"`
	ValidationResult string   `json:"validation_result,omitempty"`
	QCObservations   []string `json:"qc_observations,omitempty"`
	ReviewIssues     []string `json:"review_issues,omitempty"`
	SevereIssues     []string `json:"severe_issues,omitempty"`
}

// ContentIDSummary captures envelope-level provenance for the episode
// identification stage without duplicating per-episode outcomes already stored
// in Episodes.
type ContentIDSummary struct {
	Method               string  `json:"method,omitempty"`
	ReferenceSource      string  `json:"reference_source,omitempty"`
	ReferenceEpisodes    int     `json:"reference_episodes,omitempty"`
	TranscribedEpisodes  int     `json:"transcribed_episodes,omitempty"`
	MatchedEpisodes      int     `json:"matched_episodes,omitempty"`
	UnresolvedEpisodes   int     `json:"unresolved_episodes,omitempty"`
	LowConfidenceCount   int     `json:"low_confidence_count,omitempty"`
	ReviewThreshold      float64 `json:"review_threshold,omitempty"`
	SequenceContiguous   bool    `json:"sequence_contiguous,omitempty"`
	EpisodesSynchronized bool    `json:"episodes_synchronized,omitempty"`
	Completed            bool    `json:"completed,omitempty"`
}

// FinalValidation is the apply stage's verdict on the files the organizer
// will deliver, measured after every encoded-file rewrite has completed. It is
// persisted because staging (and the ripped source it is measured against) is
// deleted once the item completes, so the verdict cannot be recomputed later.
type FinalValidation struct {
	Entries []FinalValidationEntry `json:"entries,omitempty"`
	Passed  bool                   `json:"passed"`
}

// FinalValidationEntry is the verdict for one delivered output. FailedChecks
// names the checks that did not hold; Error records why the output could not
// be probed at all (unavailable, not failed).
type FinalValidationEntry struct {
	EpisodeKey   string       `json:"episode_key,omitempty"`
	OutputPath   string       `json:"output_path"`
	Passed       bool         `json:"passed"`
	FailedChecks []string     `json:"failed_checks,omitempty"`
	Error        string       `json:"error,omitempty"`
	AVSync       *AVSyncCheck `json:"av_sync,omitempty"`
}

// AVSyncCheck compares the primary audio's start offset relative to video in
// the ripped source against the delivered output. It is deliberately
// independent of the encoder's own sync validation, which cannot see the
// rewrites the apply stage performs after encoding.
type AVSyncCheck struct {
	SourcePath           string  `json:"source_path,omitempty"`
	SourceAudioOffsetSec float64 `json:"source_audio_offset_sec"`
	OutputAudioOffsetSec float64 `json:"output_audio_offset_sec"`
	DriftMilliseconds    float64 `json:"drift_milliseconds"`
	Passed               bool    `json:"passed"`
	Error                string  `json:"error,omitempty"`
}

// RipStats records how a fresh rip performed: which physical drive read the
// disc and its throughput. Cache-restored rips record nothing (no drive was
// involved). It feeds the per-item metrics record written at item completion.
type RipStats struct {
	Device      string  `json:"device,omitempty"`
	DriveVendor string  `json:"drive_vendor,omitempty"`
	DriveModel  string  `json:"drive_model,omitempty"`
	Bytes       int64   `json:"bytes"`
	Seconds     float64 `json:"seconds"`
	Titles      int     `json:"titles"`
}

// EncodeStats summarizes one episode's encode for the metrics record. It is
// persisted per episode because encodingstate.Snapshot is single-slot and a
// TV disc would otherwise lose every episode's stats but the last.
// TargetQuality is Reel's aggregate CRF-search summary, stored verbatim.
type EncodeStats struct {
	EpisodeKey            string             `json:"episode_key"`
	Width                 int                `json:"width,omitempty"`
	Height                int                `json:"height,omitempty"`
	HDR                   bool               `json:"hdr,omitempty"`
	ResolutionClass       string             `json:"resolution_class,omitempty"`
	VideoDurationSeconds  float64            `json:"video_duration_seconds,omitempty"`
	EncodeSeconds         float64            `json:"encode_seconds,omitempty"`
	Speed                 float64            `json:"speed,omitempty"`
	Chunks                int                `json:"chunks,omitempty"`
	Frames                int                `json:"frames,omitempty"`
	OriginalSizeBytes     int64              `json:"original_size_bytes,omitempty"`
	EncodedSizeBytes      int64              `json:"encoded_size_bytes,omitempty"`
	SizeReductionPercent  float64            `json:"size_reduction_percent,omitempty"`
	PhaseSeconds          map[string]float64 `json:"phase_seconds,omitempty"`
	WorkerMeanActive      float64            `json:"worker_mean_active,omitempty"`
	WorkerPeakActive      int                `json:"worker_peak_active,omitempty"`
	WorkerMax             int                `json:"worker_max,omitempty"`
	EncodeSlotWaitSeconds float64            `json:"encode_slot_wait_seconds,omitempty"`
	TargetQuality         json.RawMessage    `json:"target_quality,omitempty"`
	GrainTreatment        *GrainTreatment    `json:"grain_treatment,omitempty"`
}

// GrainTreatment mirrors Reel's grain-gate verdict for one encode: what the
// title's bits-at-CRF measured, which treatment (if any) the encode ran with,
// and the honest denoise ceiling the reported target-quality scores sit under.
// Unlike TargetQuality it is typed rather than raw JSON because the item audit
// renders and range-checks these fields; re-parsing a blob at every read site
// would cost more than the mirror.
type GrainTreatment struct {
	// Mode is how the treatment was decided: "auto" (the gate ran), "off"
	// (disabled), or "override" (explicit experimental flags).
	Mode            string `json:"mode,omitempty"`
	Treated         bool   `json:"treated"`
	Tier            string `json:"tier,omitempty"`
	ResolutionClass string `json:"resolution_class,omitempty"`
	Denoise         string `json:"denoise,omitempty"`
	GrainTable      string `json:"grain_table,omitempty"`
	// Reason explains a verdict the numbers alone do not (SD source, no
	// eligible sample chunks, treatment disabled or overridden).
	Reason string `json:"reason,omitempty"`

	GateCRF        float64   `json:"gate_crf,omitempty"`
	SampleChunks   []int     `json:"sample_chunks,omitempty"`
	SampleBPP      []float64 `json:"sample_bpp,omitempty"`
	MedianBPP      float64   `json:"median_bpp,omitempty"`
	LightBPPCutoff float64   `json:"light_bpp_cutoff,omitempty"`
	MedBPPCutoff   float64   `json:"med_bpp_cutoff,omitempty"`
	GateSeconds    float64   `json:"gate_seconds,omitempty"`
	CeilingSeconds float64   `json:"ceiling_seconds,omitempty"`

	// DenoiseCeilingJODMean/Min score the denoised source against the real
	// source, so they cap what the encode could deliver no matter how well the
	// CRF search scored against the denoised reference. Measured only for
	// treated titles, and best effort: nil when the measurement did not run.
	DenoiseCeilingJODMean *float64 `json:"denoise_ceiling_jod_mean,omitempty"`
	DenoiseCeilingJODMin  *float64 `json:"denoise_ceiling_jod_min,omitempty"`
	// CeilingMeasured distinguishes a measured ceiling from a skipped or
	// failed best-effort measurement; CeilingError says why it is absent.
	CeilingMeasured bool   `json:"ceiling_measured,omitempty"`
	CeilingError    string `json:"ceiling_error,omitempty"`
	// BandTopJOD is the top of Reel's configured target-quality band at
	// encode time, so ceiling judgments do not hardcode the constant.
	BandTopJOD float64 `json:"band_top_jod,omitempty"`
	// Reused marks a verdict replayed from Reel's work directory on resume:
	// the recorded timings describe the run that measured them.
	Reused bool `json:"reused,omitempty"`

	// Stage 2 (hybrid gate): titles whose fixed-CRF median lands in the
	// ambiguous band are re-measured at the quality target; these fields
	// record that measurement. GateStage is "bpp" or "tq_probe".
	GateStage          string    `json:"gate_stage,omitempty"`
	AmbiguousBPPCutoff float64   `json:"ambiguous_bpp_cutoff,omitempty"`
	Stage2DeliveredBPP []float64 `json:"stage2_delivered_bpp,omitempty"`
	Stage2MedianBPP    float64   `json:"stage2_median_bpp,omitempty"`
	Stage2Probes       int       `json:"stage2_probes,omitempty"`
	Stage2Seconds      float64   `json:"stage2_seconds,omitempty"`
	Stage2Error        string    `json:"stage2_error,omitempty"`
}

// EnvelopeAttributes holds cross-cutting flags and analysis results.
type EnvelopeAttributes struct {
	AudioAnalysis             *AudioAnalysisData  `json:"audio_analysis,omitempty"`
	SubtitleGenerationResults []SubtitleGenRecord `json:"subtitle_generation_results,omitempty"`
	ContentID                 *ContentIDSummary   `json:"content_id,omitempty"`
	FinalValidation           *FinalValidation    `json:"final_validation,omitempty"`
	Rip                       *RipStats           `json:"rip,omitempty"`
	EncodeStats               []EncodeStats       `json:"encode_stats,omitempty"`
}

// SetEncodeStats adds or replaces the encode stats for one episode key, so a
// retried encode overwrites its previous record instead of duplicating it.
func (a *EnvelopeAttributes) SetEncodeStats(stats EncodeStats) {
	for i, existing := range a.EncodeStats {
		if strings.EqualFold(existing.EpisodeKey, stats.EpisodeKey) {
			a.EncodeStats[i] = stats
			return
		}
	}
	a.EncodeStats = append(a.EncodeStats, stats)
}

// ---------------------------------------------------------------------------
// Envelope methods
// ---------------------------------------------------------------------------

// Parse deserializes JSON into an Envelope. An empty or blank input returns a
// zero-value Envelope. Parse rejects envelopes whose version is not
// CurrentVersion.
func Parse(raw string) (Envelope, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Envelope{}, nil
	}

	var env Envelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return Envelope{}, fmt.Errorf("ripspec: parse envelope: %w", err)
	}

	if env.Version != CurrentVersion {
		return Envelope{}, fmt.Errorf("ripspec: unrecognized envelope version %d (expected %d)", env.Version, CurrentVersion)
	}

	return env, nil
}

// Encode serializes the Envelope to a JSON string.
func (e *Envelope) Encode() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("ripspec: encode envelope: %w", err)
	}
	return string(data), nil
}

// AssetKeys returns the episode keys for pipeline stages. Movies return
// ["main"]; TV returns each episode's non-empty key.
func (e *Envelope) AssetKeys() []string {
	if e.Metadata.MediaType == "movie" {
		return []string{"main"}
	}
	keys := make([]string, 0, len(e.Episodes))
	for _, ep := range e.Episodes {
		if ep.Key != "" {
			keys = append(keys, ep.Key)
		}
	}
	return keys
}

// EpisodeByKey returns a pointer to the episode with the given key
// (case-insensitive). Returns nil if not found.
func (e *Envelope) EpisodeByKey(key string) *Episode {
	lower := strings.ToLower(key)
	for i := range e.Episodes {
		if strings.ToLower(e.Episodes[i].Key) == lower {
			return &e.Episodes[i]
		}
	}
	return nil
}

// ExpectedCount returns 1 for movies, len(Episodes) for TV content.
func (e *Envelope) ExpectedCount() int {
	if e.Metadata.MediaType == "movie" {
		return 1
	}
	return len(e.Episodes)
}

// AssetCounts returns per-stage completion counts:
// expected, ripped, encoded, final.
func (e *Envelope) AssetCounts() (expected, ripped, encoded, final int) {
	expected = e.ExpectedCount()
	ripped = e.Assets.CompletedAssetCount(AssetKindRipped)
	encoded = e.Assets.CompletedAssetCount(AssetKindEncoded)
	final = e.Assets.CompletedAssetCount(AssetKindFinal)
	return
}

// AppendReviewReason marks an episode for review and appends a human-readable
// reason, separated by "; " when multiple reasons accumulate.
func (e *Episode) AppendReviewReason(reason string) {
	if e == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	e.NeedsReview = true
	if existing := strings.TrimSpace(e.ReviewReason); existing != "" {
		e.ReviewReason = existing + "; " + reason
	} else {
		e.ReviewReason = reason
	}
}

// ---------------------------------------------------------------------------
// Asset methods
// ---------------------------------------------------------------------------

// IsCompleted returns true when the asset has a non-empty path and its status
// is not "failed".
func (a *Asset) IsCompleted() bool {
	return a.Path != "" && a.Status != AssetStatusFailed
}

// IsFailed returns true when the asset status is "failed".
func (a *Asset) IsFailed() bool {
	return a.Status == AssetStatusFailed
}

// ---------------------------------------------------------------------------
// Assets methods
// ---------------------------------------------------------------------------

// stageSlice returns a pointer to the slice for the given stage kind.
func (as *Assets) stageSlice(kind string) *[]Asset {
	switch kind {
	case AssetKindRipped:
		return &as.Ripped
	case AssetKindEncoded:
		return &as.Encoded
	case AssetKindSubtitled:
		return &as.Subtitled
	case AssetKindFinal:
		return &as.Final
	case AssetKindTranscript:
		return &as.Transcript
	default:
		return nil
	}
}

// AddAsset appends or replaces an asset for the given episode key at the
// specified stage. Kind is "ripped", "encoded", "subtitled", or "final".
func (as *Assets) AddAsset(kind string, asset Asset) {
	sp := as.stageSlice(kind)
	if sp == nil {
		return
	}
	for i, existing := range *sp {
		if strings.EqualFold(existing.EpisodeKey, asset.EpisodeKey) {
			(*sp)[i] = asset
			return
		}
	}
	*sp = append(*sp, asset)
}

// FindAsset locates an asset by stage and episode key (case-insensitive).
func (as *Assets) FindAsset(kind, key string) (Asset, bool) {
	sp := as.stageSlice(kind)
	if sp == nil {
		return Asset{}, false
	}
	lower := strings.ToLower(key)
	for _, a := range *sp {
		if strings.ToLower(a.EpisodeKey) == lower {
			return a, true
		}
	}
	return Asset{}, false
}

// ClearFailedAsset resets the status, error message, and path for a failed
// asset so it can be retried.
func (as *Assets) ClearFailedAsset(kind, key string) {
	sp := as.stageSlice(kind)
	if sp == nil {
		return
	}
	lower := strings.ToLower(key)
	for i, a := range *sp {
		if strings.ToLower(a.EpisodeKey) == lower {
			(*sp)[i].Status = ""
			(*sp)[i].ErrorMsg = ""
			(*sp)[i].Path = ""
			return
		}
	}
}

// CompletedAssetCount returns the number of non-failed assets with a
// non-empty path at the given stage.
func (as *Assets) CompletedAssetCount(stage string) int {
	sp := as.stageSlice(stage)
	if sp == nil {
		return 0
	}
	count := 0
	for _, a := range *sp {
		if a.IsCompleted() {
			count++
		}
	}
	return count
}

// Clone returns a deep copy of all asset lists.
func (as *Assets) Clone() Assets {
	clone := Assets{}
	if as.Ripped != nil {
		clone.Ripped = make([]Asset, len(as.Ripped))
		copy(clone.Ripped, as.Ripped)
	}
	if as.Encoded != nil {
		clone.Encoded = make([]Asset, len(as.Encoded))
		copy(clone.Encoded, as.Encoded)
	}
	if as.Subtitled != nil {
		clone.Subtitled = make([]Asset, len(as.Subtitled))
		copy(clone.Subtitled, as.Subtitled)
	}
	if as.Final != nil {
		clone.Final = make([]Asset, len(as.Final))
		copy(clone.Final, as.Final)
	}
	return clone
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// PlaceholderKey formats a placeholder episode key as "s01_001". Season and
// discIndex default to 1 if <= 0.
func PlaceholderKey(season, discIndex int) string {
	if season <= 0 {
		season = 1
	}
	if discIndex <= 0 {
		discIndex = 1
	}
	return fmt.Sprintf("s%02d_%03d", season, discIndex)
}

// EpisodeLast returns the last resolved episode number represented by the entry.
// For single-episode entries this is Episode. Unresolved entries return 0.
func (e Episode) EpisodeLast() int {
	if e.EpisodeEnd > e.Episode {
		return e.EpisodeEnd
	}
	return e.Episode
}

// HasResolvedEpisodes returns true if any episode has a resolved episode
// number (> 0).
func HasResolvedEpisodes(episodes []Episode) bool {
	for _, ep := range episodes {
		if ep.Episode > 0 {
			return true
		}
	}
	return false
}

// CountUnresolvedEpisodes returns the number of episodes without a resolved
// episode number.
func CountUnresolvedEpisodes(episodes []Episode) int {
	return countUnresolved(episodes)
}

func countUnresolved(episodes []Episode) int {
	count := 0
	for _, ep := range episodes {
		if ep.Episode <= 0 {
			count++
		}
	}
	return count
}

// Double-length episode detection is a cross-stage contract: identify orders
// a probable double-length title first in the episode list, and contentid's
// opening-double inference fires only when the double leads that order. Both
// sides must therefore agree on what "double length" means, so the single
// definition lives here.
const (
	doubleEpisodeMinRatio = 1.80
	doubleEpisodeMaxRatio = 2.40
)

// IsDoubleLength reports whether duration looks like a double-length episode
// relative to the other runtimes on the disc: between 1.8x and 2.4x their
// median. Non-positive runtimes are ignored; fewer than two comparable
// runtimes is inconclusive and reports false.
func IsDoubleLength(duration int, otherRuntimes []int) bool {
	if duration <= 0 {
		return false
	}
	rest := make([]int, 0, len(otherRuntimes))
	for _, runtime := range otherRuntimes {
		if runtime > 0 {
			rest = append(rest, runtime)
		}
	}
	if len(rest) < 2 {
		return false
	}
	slices.Sort(rest)
	median := rest[len(rest)/2]
	return duration >= int(float64(median)*doubleEpisodeMinRatio) &&
		duration <= int(float64(median)*doubleEpisodeMaxRatio)
}
