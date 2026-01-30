package config

import (
	"errors"
	"fmt"
	"strings"
)

// Validate ensures the configuration is usable.
func (c *Config) Validate() error {
	if err := c.validateTMDB(); err != nil {
		return err
	}
	if err := c.validateLibrary(); err != nil {
		return err
	}
	if err := c.validateJellyfin(); err != nil {
		return err
	}
	if err := c.validateWorkflow(); err != nil {
		return err
	}
	if err := c.validateMakeMKV(); err != nil {
		return err
	}
	if err := c.validateSubtitles(); err != nil {
		return err
	}
	if err := c.validateRipCache(); err != nil {
		return err
	}
	if err := c.validatePresetDecider(); err != nil {
		return err
	}
	if err := c.validateNotifications(); err != nil {
		return err
	}
	return nil
}

func (c *Config) validateTMDB() error {
	if c.TMDB.APIKey == "" {
		defaultPath, err := DefaultConfigPath()
		if err != nil {
			defaultPath = "~/.config/spindle/config.toml"
		}
		return fmt.Errorf("tmdb.api_key is required. Set TMDB_API_KEY env var or edit %s (create with 'spindle config init')", defaultPath)
	}
	return nil
}

func (c *Config) validateLibrary() error {
	if c.Library.MoviesDir == "" {
		return errors.New("library.movies_dir must be set")
	}
	if c.Library.TVDir == "" {
		return errors.New("library.tv_dir must be set")
	}
	return nil
}

func (c *Config) validateJellyfin() error {
	if !c.Jellyfin.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Jellyfin.URL) == "" {
		return errors.New("jellyfin.url must be set when jellyfin.enabled is true")
	}
	if strings.TrimSpace(c.Jellyfin.APIKey) == "" {
		return errors.New("jellyfin.api_key must be set when jellyfin.enabled is true")
	}
	return nil
}

func (c *Config) validateWorkflow() error {
	if err := ensurePositiveMap(map[string]int{
		"makemkv.rip_timeout":           c.MakeMKV.RipTimeout,
		"makemkv.info_timeout":          c.MakeMKV.InfoTimeout,
		"notifications.request_timeout": c.Notifications.RequestTimeout,
		"workflow.disc_monitor_timeout": c.Workflow.DiscMonitorTimeout,
		"workflow.queue_poll_interval":  c.Workflow.QueuePollInterval,
		"workflow.error_retry_interval": c.Workflow.ErrorRetryInterval,
	}); err != nil {
		return err
	}
	if c.Workflow.HeartbeatInterval <= 0 {
		return errors.New("workflow.heartbeat_interval must be positive")
	}
	if c.Workflow.HeartbeatTimeout <= 0 {
		return errors.New("workflow.heartbeat_timeout must be positive")
	}
	if c.Workflow.HeartbeatTimeout <= c.Workflow.HeartbeatInterval {
		return errors.New("workflow.heartbeat_timeout must be greater than workflow.heartbeat_interval")
	}
	return nil
}

func (c *Config) validateMakeMKV() error {
	if strings.TrimSpace(c.MakeMKV.KeyDBDownloadURL) == "" {
		return errors.New("makemkv.keydb_download_url must be set")
	}
	if c.MakeMKV.KeyDBDownloadTimeout <= 0 {
		return errors.New("makemkv.keydb_download_timeout must be positive (seconds)")
	}
	return nil
}

func (c *Config) validateSubtitles() error {
	if c.Subtitles.OpenSubtitlesEnabled {
		if strings.TrimSpace(c.Subtitles.OpenSubtitlesAPIKey) == "" {
			return errors.New("subtitles.opensubtitles_api_key must be set when subtitles.opensubtitles_enabled is true")
		}
		if strings.TrimSpace(c.Subtitles.OpenSubtitlesUserAgent) == "" {
			return errors.New("subtitles.opensubtitles_user_agent must be set when subtitles.opensubtitles_enabled is true")
		}
		if len(c.Subtitles.OpenSubtitlesLanguages) == 0 {
			return errors.New("subtitles.opensubtitles_languages must include at least one language when subtitles.opensubtitles_enabled is true")
		}
	}
	return nil
}

func (c *Config) validateRipCache() error {
	if c.RipCache.Enabled {
		if strings.TrimSpace(c.RipCache.Dir) == "" {
			return errors.New("rip_cache.dir must be set when rip_cache.enabled is true")
		}
		if c.RipCache.MaxGiB <= 0 {
			return errors.New("rip_cache.max_gib must be positive when rip_cache.enabled is true")
		}
	}
	return nil
}

func (c *Config) validatePresetDecider() error {
	if c.PresetDecider.Enabled && strings.TrimSpace(c.PresetDecider.APIKey) == "" {
		return errors.New("preset_decider.api_key must be set when preset_decider.enabled is true (or set OPENROUTER_API_KEY)")
	}
	return nil
}

func (c *Config) validateNotifications() error {
	if c.Notifications.MinRipSeconds < 0 {
		return errors.New("notifications.min_rip_seconds must be >= 0")
	}
	if c.Notifications.QueueMinItems < 1 {
		return errors.New("notifications.queue_min_items must be >= 1")
	}
	if c.Notifications.DedupWindowSeconds < 0 {
		return errors.New("notifications.dedup_window_seconds must be >= 0")
	}
	return nil
}

func ensurePositiveMap(values map[string]int) error {
	for key, value := range values {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", key)
		}
	}
	return nil
}
