package config

import (
	"fmt"
	"net/url"
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
	if strings.TrimSpace(c.Library.ShortsDir) == "" {
		errs = append(errs, "library.shorts_dir is required")
	}

	// Value ranges.
	errs = append(errs, ValidateContentID(c.ContentID)...)
	if c.MakeMKV.RipTimeout <= 0 {
		errs = append(errs, fmt.Sprintf("makemkv.rip_timeout must be > 0 (got %d)", c.MakeMKV.RipTimeout))
	}
	if c.MakeMKV.MinTitleLength < 0 {
		errs = append(errs, fmt.Sprintf("makemkv.min_title_length must be >= 0 (got %d)", c.MakeMKV.MinTitleLength))
	}
	if c.Notifications.RequestTimeout <= 0 {
		errs = append(errs, fmt.Sprintf("notifications.request_timeout must be > 0 (got %d)", c.Notifications.RequestTimeout))
	}
	if topic := strings.TrimSpace(c.Notifications.NtfyTopic); topic != "" {
		parsed, err := url.Parse(topic)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
			errs = append(errs, "notifications.ntfy_topic must be an absolute http or https topic URL")
		}
	}

	if c.Loom.URL != "" {
		parsed, err := url.Parse(c.Loom.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			errs = append(errs, "loom.url must be an absolute http or https URL without a query or fragment")
		}
	}

	// Conditional requirements.
	if c.Subtitles.Enabled && c.Subtitles.WhisperXVADMethod != "silero" {
		if c.Subtitles.WhisperXHFToken == "" {
			errs = append(errs, "subtitles.whisperx_hf_token is required when subtitles enabled with non-silero VAD method")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ValidateContentID checks episode identification threshold ranges.
func ValidateContentID(cid ContentIDConfig) []string {
	var errs []string
	for _, pair := range []struct {
		name string
		val  float64
	}{
		{"content_id.min_similarity_score", cid.MinSimilarityScore},
		{"content_id.clear_match_margin", cid.ClearMatchMargin},
		{"content_id.low_confidence_review_threshold", cid.LowConfidenceReviewThreshold},
		{"content_id.decisive_auto_accept_threshold", cid.DecisiveAutoAcceptThreshold},
		{"content_id.clear_confidence_threshold", cid.ClearConfidenceThreshold},
	} {
		if pair.val <= 0 || pair.val >= 1 {
			errs = append(errs, fmt.Sprintf("%s must be > 0 and < 1 (got %.2f)", pair.name, pair.val))
		}
	}
	if cid.DecisiveAutoAcceptThreshold <= cid.LowConfidenceReviewThreshold || cid.DecisiveAutoAcceptThreshold > cid.ClearConfidenceThreshold {
		errs = append(errs, "content_id.decisive_auto_accept_threshold must be > low_confidence_review_threshold and <= clear_confidence_threshold")
	}
	return errs
}
