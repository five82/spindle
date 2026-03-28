package ripspec

import (
	"encoding/json"
	"fmt"
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
	Edition      string  `json:"edition,omitempty"`
	Filename     string  `json:"filename,omitempty"`
	DiscSource   string  `json:"disc_source,omitempty"`
}

// Title represents a MakeMKV title on the disc.
type Title struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Duration       int    `json:"duration"`
	Chapters       int    `json:"chapters"`
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
	EpisodeTitle    string  `json:"episode_title,omitempty"`
	EpisodeAirDate  string  `json:"episode_air_date,omitempty"`
	RuntimeSeconds  int     `json:"runtime_seconds,omitempty"`
	TitleHash       string  `json:"title_hash,omitempty"`
	OutputBasename  string  `json:"output_basename,omitempty"`
	MatchConfidence float64 `json:"match_confidence,omitempty"`
}

// Asset represents a single file artifact at a pipeline stage.
type Asset struct {
	EpisodeKey     string `json:"episode_key"`
	TitleID        int    `json:"title_id,omitempty"`
	Path           string `json:"path"`
	Status         string `json:"status"`
	SubtitlesMuxed bool   `json:"subtitles_muxed,omitempty"`
	ErrorMsg       string `json:"error_msg,omitempty"`
}

// Assets holds per-stage asset lists.
type Assets struct {
	Ripped    []Asset `json:"ripped,omitempty"`
	Encoded   []Asset `json:"encoded,omitempty"`
	Subtitled []Asset `json:"subtitled,omitempty"`
	Final     []Asset `json:"final,omitempty"`
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

// AudioAnalysisData holds the results of audio track analysis.
type AudioAnalysisData struct {
	PrimaryTrack        AudioTrackRef        `json:"primary_track"`
	PrimaryDescription  string               `json:"primary_description,omitempty"`
	CommentaryTracks    []CommentaryTrackRef `json:"commentary_tracks,omitempty"`
	ExcludedTracks      []ExcludedTrackRef   `json:"excluded_tracks,omitempty"`
}

// SubtitleGenRecord captures the result of subtitle generation for one episode.
type SubtitleGenRecord struct {
	EpisodeKey            string   `json:"episode_key"`
	Source                string   `json:"source"`
	Cached                bool     `json:"cached,omitempty"`
	SubtitlePath          string   `json:"subtitle_path"`
	Segments              int      `json:"segments"`
	DurationSec           float64  `json:"duration_sec,omitempty"`
	Language              string   `json:"language"`
	OpenSubtitlesDecision string   `json:"opensubtitles_decision,omitempty"`
	ValidationIssues      []string `json:"validation_issues,omitempty"`
}

// EnvelopeAttributes holds cross-cutting flags and analysis results.
type EnvelopeAttributes struct {
	HasForcedSubtitleTrack    bool                `json:"has_forced_subtitle_track,omitempty"`
	AudioAnalysis             *AudioAnalysisData  `json:"audio_analysis,omitempty"`
	SubtitleGenerationResults []SubtitleGenRecord `json:"subtitle_generation_results,omitempty"`
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
	ripped = e.Assets.CompletedAssetCount("ripped")
	encoded = e.Assets.CompletedAssetCount("encoded")
	final = e.Assets.CompletedAssetCount("final")
	return
}

// MissingEpisodes returns episode keys that do not have a completed asset at
// the given stage. Returns nil for movies. Stage is one of "ripped",
// "encoded", "subtitled", or "final".
func (e *Envelope) MissingEpisodes(stage string) []string {
	if e.Metadata.MediaType == "movie" {
		return nil
	}

	var missing []string
	for _, ep := range e.Episodes {
		asset, found := e.Assets.FindAsset(stage, ep.Key)
		if !found || !asset.IsCompleted() {
			missing = append(missing, ep.Key)
		}
	}
	return missing
}

// ---------------------------------------------------------------------------
// Asset methods
// ---------------------------------------------------------------------------

// IsCompleted returns true when the asset has a non-empty path and its status
// is not "failed".
func (a *Asset) IsCompleted() bool {
	return a.Path != "" && a.Status != "failed"
}

// IsFailed returns true when the asset status is "failed".
func (a *Asset) IsFailed() bool {
	return a.Status == "failed"
}

// ---------------------------------------------------------------------------
// Assets methods
// ---------------------------------------------------------------------------

// stageSlice returns a pointer to the slice for the given stage kind.
func (as *Assets) stageSlice(kind string) *[]Asset {
	switch kind {
	case "ripped":
		return &as.Ripped
	case "encoded":
		return &as.Encoded
	case "subtitled":
		return &as.Subtitled
	case "final":
		return &as.Final
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

// EpisodeKey formats an episode key as "s01e03". Returns "" if both season
// and episode are <= 0.
func EpisodeKey(season, episode int) string {
	if season <= 0 && episode <= 0 {
		return ""
	}
	return fmt.Sprintf("s%02de%02d", season, episode)
}

// HasResolvedEpisodes returns true if any episode has a non-empty Key whose
// format matches an episode key (contains "e") rather than a placeholder.
func HasResolvedEpisodes(episodes []Episode) bool {
	for _, ep := range episodes {
		if ep.Key != "" && strings.Contains(ep.Key, "e") {
			return true
		}
	}
	return false
}

// HasUnresolvedEpisodes returns true if any episode has an empty Key or a
// placeholder key (no "e" separator).
func HasUnresolvedEpisodes(episodes []Episode) bool {
	return countUnresolved(episodes) > 0
}

// CountUnresolvedEpisodes returns the number of episodes with empty or
// placeholder keys.
func CountUnresolvedEpisodes(episodes []Episode) int {
	return countUnresolved(episodes)
}

func countUnresolved(episodes []Episode) int {
	count := 0
	for _, ep := range episodes {
		if ep.Key == "" || !strings.Contains(ep.Key, "e") {
			count++
		}
	}
	return count
}
