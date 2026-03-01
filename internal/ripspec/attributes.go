package ripspec

import "strings"

// EnvelopeAttributes carries typed cross-stage communication data.
// JSON field names match the keys previously used in the map[string]any
// representation for backward compatibility.
type EnvelopeAttributes struct {
	// Identification stage
	DiscNumber             int  `json:"disc_number,omitempty"`
	HasForcedSubtitleTrack bool `json:"has_forced_subtitle_track,omitempty"`

	// Content ID stage
	ContentIDNeedsReview      bool              `json:"content_id_needs_review,omitempty"`
	ContentIDReviewReason     string            `json:"content_id_review_reason,omitempty"`
	ContentIDMatches          []ContentIDMatch  `json:"content_id_matches,omitempty"`
	ContentIDMethod           string            `json:"content_id_method,omitempty"`
	ContentIDTranscripts      map[string]string `json:"content_id_transcripts,omitempty"`
	ContentIDSelectedStrategy string            `json:"content_id_selected_strategy,omitempty"`
	ContentIDStrategyScores   []StrategyScore   `json:"content_id_strategy_scores,omitempty"`
	EpisodesSynchronized      bool              `json:"episodes_synchronized,omitempty"`

	// Audio analysis stage
	PrimaryAudioDescription string             `json:"primary_audio_description,omitempty"`
	AudioAnalysis           *AudioAnalysisData `json:"audio_analysis,omitempty"`

	// Subtitle stage
	SubtitleGenerationResults []SubtitleGenRecord `json:"subtitle_generation_results,omitempty"`
	SubtitleGenerationSummary *SubtitleGenSummary `json:"subtitle_generation_summary,omitempty"`
}

// AppendReviewReason sets the needs-review flag and appends a reason,
// separated by "; " from any existing reason.
func (a *EnvelopeAttributes) AppendReviewReason(reason string) {
	if a == nil {
		return
	}
	a.ContentIDNeedsReview = true
	if existing := strings.TrimSpace(a.ContentIDReviewReason); existing != "" {
		a.ContentIDReviewReason = existing + "; " + reason
	} else {
		a.ContentIDReviewReason = reason
	}
}

// ContentIDMatch records a single episode-to-reference match from content ID.
type ContentIDMatch struct {
	EpisodeKey        string  `json:"episode_key"`
	TitleID           int     `json:"title_id"`
	MatchedEpisode    int     `json:"matched_episode"`
	Score             float64 `json:"score"`
	SubtitleFileID    int64   `json:"subtitle_file_id,omitempty"`
	SubtitleLanguage  string  `json:"subtitle_language,omitempty"`
	SubtitleCachePath string  `json:"subtitle_cache_path,omitempty"`
}

// StrategyScore records the evaluation outcome for a single matching strategy.
type StrategyScore struct {
	Strategy     string  `json:"strategy"`
	Reason       string  `json:"reason"`
	EpisodeCount int     `json:"episode_count"`
	References   int     `json:"references"`
	Matches      int     `json:"matches"`
	AvgScore     float64 `json:"avg_score"`
	NeedsReview  bool    `json:"needs_review"`
}

// AudioAnalysisData captures the results of audio track analysis.
type AudioAnalysisData struct {
	PrimaryTrack     AudioTrackRef        `json:"primary_track"`
	CommentaryTracks []CommentaryTrackRef `json:"commentary_tracks,omitempty"`
	ExcludedTracks   []ExcludedTrackRef   `json:"excluded_tracks,omitempty"`
}

// AudioTrackRef identifies an audio track by its stream index.
type AudioTrackRef struct {
	Index int `json:"index"`
}

// CommentaryTrackRef describes a detected commentary track.
type CommentaryTrackRef struct {
	Index      int     `json:"index"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// ExcludedTrackRef describes a track excluded from commentary detection.
type ExcludedTrackRef struct {
	Index      int     `json:"index"`
	Reason     string  `json:"reason"`
	Similarity float64 `json:"similarity,omitempty"`
}

// SubtitleGenRecord captures per-episode subtitle generation results.
type SubtitleGenRecord struct {
	EpisodeKey            string `json:"episode_key"`
	Source                string `json:"source"`
	Cached                bool   `json:"cached,omitempty"`
	SubtitlePath          string `json:"subtitle_path,omitempty"`
	Segments              int    `json:"segments,omitempty"`
	Language              string `json:"language,omitempty"`
	OpenSubtitlesDecision string `json:"opensubtitles_decision,omitempty"`
}

// SubtitleGenSummary captures aggregate subtitle generation statistics.
type SubtitleGenSummary struct {
	Source                string `json:"source,omitempty"`
	OpenSubtitles         int    `json:"opensubtitles,omitempty"`
	WhisperX              int    `json:"whisperx,omitempty"`
	ExpectedOpenSubtitles bool   `json:"expected_opensubtitles,omitempty"`
	FallbackUsed          bool   `json:"fallback_used,omitempty"`
}
