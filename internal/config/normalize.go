package config

import (
	"fmt"
	"os"
	"strings"
)

func (c *Config) normalize() error {
	if err := c.normalizePaths(); err != nil {
		return err
	}
	if err := c.normalizeTMDB(); err != nil {
		return err
	}
	if err := c.normalizeJellyfin(); err != nil {
		return err
	}
	if err := c.normalizeSubtitles(); err != nil {
		return err
	}
	if err := c.normalizeRipCache(); err != nil {
		return err
	}
	if err := c.normalizeMakeMKV(); err != nil {
		return err
	}
	if err := c.normalizePresetDecider(); err != nil {
		return err
	}
	c.normalizeLogging()
	return nil
}

func (c *Config) normalizePaths() error {
	var err error
	if c.Paths.StagingDir, err = expandPath(c.Paths.StagingDir); err != nil {
		return fmt.Errorf("paths.staging_dir: %w", err)
	}
	if c.Paths.LibraryDir, err = expandPath(c.Paths.LibraryDir); err != nil {
		return fmt.Errorf("paths.library_dir: %w", err)
	}
	if c.Paths.LogDir, err = expandPath(c.Paths.LogDir); err != nil {
		return fmt.Errorf("paths.log_dir: %w", err)
	}
	if c.Paths.ReviewDir, err = expandPath(c.Paths.ReviewDir); err != nil {
		return fmt.Errorf("paths.review_dir: %w", err)
	}
	if strings.TrimSpace(c.Paths.OpenSubtitlesCacheDir) == "" {
		c.Paths.OpenSubtitlesCacheDir = defaultOpenSubtitlesCacheDir
	}
	if c.Paths.OpenSubtitlesCacheDir, err = expandPath(c.Paths.OpenSubtitlesCacheDir); err != nil {
		return fmt.Errorf("paths.opensubtitles_cache_dir: %w", err)
	}
	if strings.TrimSpace(c.Paths.WhisperXCacheDir) == "" {
		c.Paths.WhisperXCacheDir = defaultWhisperXCacheDir
	}
	if c.Paths.WhisperXCacheDir, err = expandPath(c.Paths.WhisperXCacheDir); err != nil {
		return fmt.Errorf("paths.whisperx_cache_dir: %w", err)
	}
	c.Paths.APIBind = strings.TrimSpace(c.Paths.APIBind)
	if c.Paths.APIBind == "" {
		c.Paths.APIBind = defaultAPIBind
	}
	return nil
}

func (c *Config) normalizeTMDB() error {
	if c.TMDB.APIKey == "" {
		if value, ok := os.LookupEnv("TMDB_API_KEY"); ok {
			c.TMDB.APIKey = value
		}
	}
	c.TMDB.BaseURL = strings.TrimSpace(c.TMDB.BaseURL)
	if c.TMDB.BaseURL == "" {
		c.TMDB.BaseURL = defaultTMDBBaseURL
	}
	return nil
}

func (c *Config) normalizeJellyfin() error {
	if c.Jellyfin.APIKey == "" {
		if value, ok := os.LookupEnv("JELLYFIN_API_KEY"); ok {
			c.Jellyfin.APIKey = strings.TrimSpace(value)
		}
	}
	c.Jellyfin.URL = strings.TrimSpace(c.Jellyfin.URL)
	c.Jellyfin.APIKey = strings.TrimSpace(c.Jellyfin.APIKey)
	return nil
}

func (c *Config) normalizeSubtitles() error {
	c.Subtitles.WhisperXVADMethod = strings.ToLower(strings.TrimSpace(c.Subtitles.WhisperXVADMethod))
	if c.Subtitles.WhisperXVADMethod == "" {
		c.Subtitles.WhisperXVADMethod = "silero"
	}
	c.Subtitles.WhisperXHuggingFace = strings.TrimSpace(c.Subtitles.WhisperXHuggingFace)
	if c.Subtitles.WhisperXHuggingFace == "" {
		if value, ok := os.LookupEnv("HUGGING_FACE_HUB_TOKEN"); ok {
			c.Subtitles.WhisperXHuggingFace = strings.TrimSpace(value)
		} else if value, ok := os.LookupEnv("HF_TOKEN"); ok {
			c.Subtitles.WhisperXHuggingFace = strings.TrimSpace(value)
		}
	}
	c.Subtitles.OpenSubtitlesAPIKey = strings.TrimSpace(c.Subtitles.OpenSubtitlesAPIKey)
	if c.Subtitles.OpenSubtitlesAPIKey == "" {
		if value, ok := os.LookupEnv("OPENSUBTITLES_API_KEY"); ok {
			c.Subtitles.OpenSubtitlesAPIKey = strings.TrimSpace(value)
		}
	}
	c.Subtitles.OpenSubtitlesUserAgent = strings.TrimSpace(c.Subtitles.OpenSubtitlesUserAgent)
	if c.Subtitles.OpenSubtitlesUserAgent == "" {
		c.Subtitles.OpenSubtitlesUserAgent = defaultOpenSubtitlesUserAgent
	}
	c.Subtitles.OpenSubtitlesUserToken = strings.TrimSpace(c.Subtitles.OpenSubtitlesUserToken)
	if c.Subtitles.OpenSubtitlesUserToken == "" {
		if value, ok := os.LookupEnv("OPENSUBTITLES_USER_TOKEN"); ok {
			c.Subtitles.OpenSubtitlesUserToken = strings.TrimSpace(value)
		}
	}
	if len(c.Subtitles.OpenSubtitlesLanguages) == 0 {
		c.Subtitles.OpenSubtitlesLanguages = []string{"en"}
	} else {
		langs := make([]string, 0, len(c.Subtitles.OpenSubtitlesLanguages))
		seen := make(map[string]struct{}, len(c.Subtitles.OpenSubtitlesLanguages))
		for _, lang := range c.Subtitles.OpenSubtitlesLanguages {
			normalized := strings.ToLower(strings.TrimSpace(lang))
			if normalized == "" {
				continue
			}
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			langs = append(langs, normalized)
		}
		if len(langs) == 0 {
			langs = []string{"en"}
		}
		c.Subtitles.OpenSubtitlesLanguages = langs
	}
	return nil
}

func (c *Config) normalizeRipCache() error {
	var err error
	if strings.TrimSpace(c.RipCache.Dir) == "" {
		c.RipCache.Dir = defaultRipCacheDir()
	}
	if c.RipCache.Dir, err = expandPath(c.RipCache.Dir); err != nil {
		return fmt.Errorf("rip_cache.dir: %w", err)
	}
	if c.RipCache.MaxGiB <= 0 {
		c.RipCache.MaxGiB = defaultRipCacheMaxGiB
	}
	return nil
}

func (c *Config) normalizeMakeMKV() error {
	var err error
	if c.MakeMKV.KeyDBPath, err = expandPath(c.MakeMKV.KeyDBPath); err != nil {
		return fmt.Errorf("makemkv.keydb_path: %w", err)
	}
	if strings.TrimSpace(c.MakeMKV.IdentificationOverridesPath) == "" {
		c.MakeMKV.IdentificationOverridesPath = defaultIdentificationOverridesPath
	}
	if c.MakeMKV.IdentificationOverridesPath, err = expandPath(c.MakeMKV.IdentificationOverridesPath); err != nil {
		return fmt.Errorf("makemkv.identification_overrides_path: %w", err)
	}
	if strings.TrimSpace(c.MakeMKV.KeyDBDownloadURL) == "" {
		c.MakeMKV.KeyDBDownloadURL = defaultKeyDBDownloadURL
	}
	c.MakeMKV.KeyDBDownloadURL = strings.TrimSpace(c.MakeMKV.KeyDBDownloadURL)
	if c.MakeMKV.KeyDBDownloadTimeout <= 0 {
		c.MakeMKV.KeyDBDownloadTimeout = defaultKeyDBDownloadTimeout
	}
	return nil
}

func (c *Config) normalizePresetDecider() error {
	c.PresetDecider.BaseURL = strings.TrimSpace(c.PresetDecider.BaseURL)
	if c.PresetDecider.BaseURL == "" {
		c.PresetDecider.BaseURL = defaultPresetDeciderBaseURL
	}
	c.PresetDecider.Model = strings.TrimSpace(c.PresetDecider.Model)
	if c.PresetDecider.Model == "" {
		c.PresetDecider.Model = defaultPresetDeciderModel
	}
	c.PresetDecider.Referer = strings.TrimSpace(c.PresetDecider.Referer)
	if c.PresetDecider.Referer == "" {
		c.PresetDecider.Referer = defaultPresetDeciderReferer
	}
	c.PresetDecider.Title = strings.TrimSpace(c.PresetDecider.Title)
	if c.PresetDecider.Title == "" {
		c.PresetDecider.Title = defaultPresetDeciderTitle
	}
	if c.PresetDecider.TimeoutSeconds <= 0 {
		c.PresetDecider.TimeoutSeconds = defaultPresetDeciderTimeoutSeconds
	}
	c.PresetDecider.APIKey = strings.TrimSpace(c.PresetDecider.APIKey)
	if c.PresetDecider.APIKey == "" {
		if value, ok := os.LookupEnv("PRESET_DECIDER_API_KEY"); ok {
			c.PresetDecider.APIKey = strings.TrimSpace(value)
		} else if value, ok := os.LookupEnv("OPENROUTER_API_KEY"); ok {
			c.PresetDecider.APIKey = strings.TrimSpace(value)
		} else if value, ok := os.LookupEnv("DEEPSEEK_API_KEY"); ok {
			c.PresetDecider.APIKey = strings.TrimSpace(value)
		}
	}
	return nil
}

func (c *Config) normalizeLogging() {
	c.Logging.Format = strings.ToLower(strings.TrimSpace(c.Logging.Format))
	switch c.Logging.Format {
	case "", "console":
		c.Logging.Format = "console"
	case "json":
	default:
		c.Logging.Format = "console"
	}
	c.Logging.Level = strings.ToLower(strings.TrimSpace(c.Logging.Level))
	if c.Logging.Level == "" {
		c.Logging.Level = defaultLogLevel
	}
	if c.Logging.RetentionDays < 0 {
		c.Logging.RetentionDays = 0
	}
}
