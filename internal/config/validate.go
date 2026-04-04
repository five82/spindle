package config

import (
	"fmt"
	"strings"
)

// Validate checks all configuration constraints and returns all errors joined.
func (c *Config) Validate() error {
	var errs []string

	// Required fields.
	if c.TMDB.APIKey == "" {
		errs = append(errs, "tmdb.api_key is required")
	}
	if c.Paths.StagingDir == "" {
		errs = append(errs, "paths.staging_dir is required")
	}
	if c.Paths.StateDir == "" {
		errs = append(errs, "paths.state_dir is required")
	}
	if c.Paths.ReviewDir == "" {
		errs = append(errs, "paths.review_dir is required")
	}

	// Value ranges.
	errs = append(errs, ValidateEncoding(c.Encoding)...)
	errs = append(errs, ValidateContentID(c.ContentID)...)
	if c.MakeMKV.RipTimeout <= 0 {
		errs = append(errs, fmt.Sprintf("makemkv.rip_timeout must be > 0 (got %d)", c.MakeMKV.RipTimeout))
	}
	if c.MakeMKV.MinTitleLength < 0 {
		errs = append(errs, fmt.Sprintf("makemkv.min_title_length must be >= 0 (got %d)", c.MakeMKV.MinTitleLength))
	}

	// Conditional requirements.
	if c.Jellyfin.Enabled {
		if c.Jellyfin.URL == "" {
			errs = append(errs, "jellyfin.url is required when jellyfin.enabled")
		}
		if c.Jellyfin.APIKey == "" {
			errs = append(errs, "jellyfin.api_key is required when jellyfin.enabled")
		}
	}

	if c.Subtitles.Enabled && c.Subtitles.WhisperXVADMethod != "silero" {
		if c.Subtitles.WhisperXHFToken == "" {
			errs = append(errs, "subtitles.whisperx_hf_token is required when subtitles enabled with non-silero VAD method")
		}
	}

	if c.Subtitles.OpenSubtitlesEnabled {
		if c.Subtitles.OpenSubtitlesAPIKey == "" {
			errs = append(errs, "subtitles.opensubtitles_api_key is required when opensubtitles enabled")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ValidateEncoding checks encoding-specific field ranges and returns error strings.
// Used by both Validate and ReloadEncoding to avoid duplicating range checks.
func ValidateEncoding(enc EncodingConfig) []string {
	var errs []string
	if enc.SVTAV1Preset < 0 || enc.SVTAV1Preset > 13 {
		errs = append(errs, fmt.Sprintf("encoding.svt_av1_preset must be 0-13 (got %d)", enc.SVTAV1Preset))
	}
	for _, pair := range []struct {
		name string
		val  int
	}{
		{"encoding.crf_sd", enc.CRFSD},
		{"encoding.crf_hd", enc.CRFHD},
		{"encoding.crf_uhd", enc.CRFUHD},
	} {
		if pair.val < 0 || pair.val > 63 {
			errs = append(errs, fmt.Sprintf("%s must be 0-63 (got %d)", pair.name, pair.val))
		}
	}
	return errs
}

// applyEncodingDefaults sets zero-value encoding fields to their defaults.
func applyEncodingDefaults(enc *EncodingConfig) {
	if enc.SVTAV1Preset == 0 {
		enc.SVTAV1Preset = 6
	}
}

// ValidateContentID checks episode identification threshold ranges.
func ValidateContentID(cid ContentIDConfig) []string {
	var errs []string
	for _, pair := range []struct {
		name string
		val  float64
	}{
		{"content_id.min_similarity_score", cid.MinSimilarityScore},
		{"content_id.low_confidence_review_threshold", cid.LowConfidenceReviewThreshold},
		{"content_id.llm_verify_threshold", cid.LLMVerifyThreshold},
		{"content_id.anchor_min_score", cid.AnchorMinScore},
		{"content_id.anchor_min_score_margin", cid.AnchorMinScoreMargin},
		{"content_id.block_high_confidence_delta", cid.BlockHighConfidenceDelta},
		{"content_id.block_high_confidence_top_ratio", cid.BlockHighConfidenceTopRatio},
	} {
		if pair.val <= 0 || pair.val >= 1 {
			errs = append(errs, fmt.Sprintf("%s must be > 0 and < 1 (got %.2f)", pair.name, pair.val))
		}
	}
	for _, pair := range []struct {
		name string
		val  int
	}{
		{"content_id.disc_block_padding_min", cid.DiscBlockPaddingMin},
		{"content_id.disc_block_padding_divisor", cid.DiscBlockPaddingDivisor},
		{"content_id.disc2_plus_min_start_episode", cid.Disc2PlusMinStartEpisode},
	} {
		if pair.val <= 0 {
			errs = append(errs, fmt.Sprintf("%s must be > 0 (got %d)", pair.name, pair.val))
		}
	}
	return errs
}
