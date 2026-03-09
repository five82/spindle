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
	if c.Encoding.SVTAV1Preset < 0 || c.Encoding.SVTAV1Preset > 13 {
		errs = append(errs, fmt.Sprintf("encoding.svt_av1_preset must be 0-13 (got %d)", c.Encoding.SVTAV1Preset))
	}
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
