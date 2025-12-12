package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed sample_config.toml
var sampleConfig string

// Config encapsulates all configuration values for the Go implementation of Spindle.
type Config struct {
	StagingDir                    string   `toml:"staging_dir"`
	LibraryDir                    string   `toml:"library_dir"`
	LogDir                        string   `toml:"log_dir"`
	LogRetentionDays              int      `toml:"log_retention_days"`
	OpenSubtitlesCacheDir         string   `toml:"opensubtitles_cache_dir"`
	WhisperXCacheDir              string   `toml:"whisperx_cache_dir"`
	ReviewDir                     string   `toml:"review_dir"`
	RipCacheEnabled               bool     `toml:"rip_cache_enabled"`
	RipCacheDir                   string   `toml:"rip_cache_dir"`
	RipCacheMaxGiB                int      `toml:"rip_cache_max_gib"`
	OpticalDrive                  string   `toml:"optical_drive"`
	APIBind                       string   `toml:"api_bind"`
	TMDBAPIKey                    string   `toml:"tmdb_api_key"`
	TMDBBaseURL                   string   `toml:"tmdb_base_url"`
	TMDBLanguage                  string   `toml:"tmdb_language"`
	TMDBConfidenceThreshold       float64  `toml:"tmdb_confidence_threshold"`
	MoviesDir                     string   `toml:"movies_dir"`
	TVDir                         string   `toml:"tv_dir"`
	PlexLinkEnabled               bool     `toml:"plex_link_enabled"`
	PlexURL                       string   `toml:"plex_url"`
	PlexAuthPath                  string   `toml:"plex_auth_path"`
	OverwriteExistingLibraryFiles bool     `toml:"overwrite_existing_library_files"`
	MoviesLibrary                 string   `toml:"movies_library"`
	TVLibrary                     string   `toml:"tv_library"`
	NtfyTopic                     string   `toml:"ntfy_topic"`
	KeyDBPath                     string   `toml:"keydb_path"`
	KeyDBDownloadURL              string   `toml:"keydb_download_url"`
	KeyDBDownloadTimeout          int      `toml:"keydb_download_timeout"`
	IdentificationOverridesPath   string   `toml:"identification_overrides_path"`
	MakeMKVRipTimeout             int      `toml:"makemkv_rip_timeout"`
	MakeMKVInfoTimeout            int      `toml:"makemkv_info_timeout"`
	NtfyRequestTimeout            int      `toml:"ntfy_request_timeout"`
	DiscMonitorTimeout            int      `toml:"disc_monitor_timeout"`
	QueuePollInterval             int      `toml:"queue_poll_interval"`
	ErrorRetryInterval            int      `toml:"error_retry_interval"`
	WorkflowHeartbeatInterval     int      `toml:"workflow_heartbeat_interval"`
	WorkflowHeartbeatTimeout      int      `toml:"workflow_heartbeat_timeout"`
	LogFormat                     string   `toml:"log_format"`
	LogLevel                      string   `toml:"log_level"`
	SubtitlesEnabled              bool     `toml:"subtitles_enabled"`
	WhisperXCUDAEnabled           bool     `toml:"whisperx_cuda_enabled"`
	WhisperXVADMethod             string   `toml:"whisperx_vad_method"`
	WhisperXHuggingFaceToken      string   `toml:"whisperx_hf_token"`
	NotifyIdentification          bool     `toml:"notify_identification"`
	NotifyRip                     bool     `toml:"notify_rip"`
	NotifyEncoding                bool     `toml:"notify_encoding"`
	NotifyOrganization            bool     `toml:"notify_organization"`
	NotifyQueue                   bool     `toml:"notify_queue"`
	NotifyReview                  bool     `toml:"notify_review"`
	NotifyErrors                  bool     `toml:"notify_errors"`
	NotifyMinRipSeconds           int      `toml:"notify_min_rip_seconds"`
	NotifyQueueMinItems           int      `toml:"notify_queue_min_items"`
	NotifyDedupWindowSeconds      int      `toml:"notify_dedup_window_seconds"`
	OpenSubtitlesEnabled          bool     `toml:"opensubtitles_enabled"`
	OpenSubtitlesAPIKey           string   `toml:"opensubtitles_api_key"`
	OpenSubtitlesUserAgent        string   `toml:"opensubtitles_user_agent"`
	OpenSubtitlesUserToken        string   `toml:"opensubtitles_user_token"`
	OpenSubtitlesLanguages        []string `toml:"opensubtitles_languages"`
	DeepSeekPresetDeciderEnabled  bool     `toml:"deepseek_preset_decider_enabled"`
	DeepSeekAPIKey                string   `toml:"deepseek_api_key"`
	PresetDeciderEnabled          bool     `toml:"preset_decider_enabled"`
	PresetDeciderAPIKey           string   `toml:"preset_decider_api_key"`
	PresetDeciderBaseURL          string   `toml:"preset_decider_base_url"`
	PresetDeciderModel            string   `toml:"preset_decider_model"`
	PresetDeciderReferer          string   `toml:"preset_decider_referer"`
	PresetDeciderTitle            string   `toml:"preset_decider_title"`
}

const (
	defaultStagingDir                  = "~/.local/share/spindle/staging"
	defaultLibraryDir                  = "~/library"
	defaultLogDir                      = "~/.local/share/spindle/logs"
	defaultLogRetentionDays            = 60
	defaultOpenSubtitlesCacheDir       = "~/.local/share/spindle/cache/opensubtitles"
	defaultWhisperXCacheDir            = "~/.local/share/spindle/cache/whisperx"
	defaultReviewDir                   = "~/review"
	defaultOpticalDrive                = "/dev/sr0"
	defaultMoviesDir                   = "movies"
	defaultTVDir                       = "tv"
	defaultTMDBLanguage                = "en-US"
	defaultTMDBBaseURL                 = "https://api.themoviedb.org/3"
	defaultLogFormat                   = "console"
	defaultLogLevel                    = "info"
	defaultWorkflowHeartbeatInterval   = 15
	defaultWorkflowHeartbeatTimeout    = 120
	defaultAPIBind                     = "127.0.0.1:7487"
	defaultNotifyMinRipSeconds         = 120
	defaultNotifyQueueMinItems         = 2
	defaultNotifyDedupWindowSeconds    = 600
	defaultPlexAuthPath                = "~/.config/spindle/plex_auth.json"
	defaultKeyDBPath                   = "~/.config/spindle/keydb/KEYDB.cfg"
	defaultKeyDBDownloadURL            = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"
	defaultKeyDBDownloadTimeout        = 300
	defaultIdentificationOverridesPath = "~/.config/spindle/overrides/identification.json"
	defaultOpenSubtitlesUserAgent      = "Spindle/dev"
	defaultRipCacheMaxGiB              = 150
	defaultPresetDeciderBaseURL        = "https://openrouter.ai/api/v1/chat/completions"
	defaultPresetDeciderModel          = "deepseek/deepseek-v3.2"
	defaultPresetDeciderReferer        = "https://github.com/five82/spindle"
	defaultPresetDeciderTitle          = "Spindle Preset Decider"
)

// Default returns a Config populated with repository defaults.
func Default() Config {
	return Config{
		StagingDir:                  defaultStagingDir,
		LibraryDir:                  defaultLibraryDir,
		LogDir:                      defaultLogDir,
		LogRetentionDays:            defaultLogRetentionDays,
		OpenSubtitlesCacheDir:       defaultOpenSubtitlesCacheDir,
		WhisperXCacheDir:            defaultWhisperXCacheDir,
		ReviewDir:                   defaultReviewDir,
		RipCacheEnabled:             false,
		RipCacheDir:                 defaultRipCacheDir(),
		RipCacheMaxGiB:              defaultRipCacheMaxGiB,
		OpticalDrive:                defaultOpticalDrive,
		APIBind:                     defaultAPIBind,
		TMDBLanguage:                defaultTMDBLanguage,
		TMDBBaseURL:                 defaultTMDBBaseURL,
		TMDBConfidenceThreshold:     0.8,
		MoviesDir:                   defaultMoviesDir,
		TVDir:                       defaultTVDir,
		PlexLinkEnabled:             true,
		MoviesLibrary:               "Movies",
		TVLibrary:                   "TV Shows",
		PlexAuthPath:                defaultPlexAuthPath,
		KeyDBPath:                   defaultKeyDBPath,
		KeyDBDownloadURL:            defaultKeyDBDownloadURL,
		KeyDBDownloadTimeout:        defaultKeyDBDownloadTimeout,
		IdentificationOverridesPath: defaultIdentificationOverridesPath,
		MakeMKVRipTimeout:           3600,
		MakeMKVInfoTimeout:          300,
		NtfyRequestTimeout:          10,
		DiscMonitorTimeout:          5,
		QueuePollInterval:           5,
		ErrorRetryInterval:          10,
		WorkflowHeartbeatInterval:   defaultWorkflowHeartbeatInterval,
		WorkflowHeartbeatTimeout:    defaultWorkflowHeartbeatTimeout,
		LogFormat:                   defaultLogFormat,
		LogLevel:                    defaultLogLevel,
		WhisperXVADMethod:           "silero",
		OpenSubtitlesLanguages:      []string{"en"},
		OpenSubtitlesUserAgent:      defaultOpenSubtitlesUserAgent,
		NotifyIdentification:        true,
		NotifyRip:                   true,
		NotifyEncoding:              true,
		NotifyOrganization:          true,
		NotifyQueue:                 true,
		NotifyReview:                true,
		NotifyErrors:                true,
		NotifyMinRipSeconds:         defaultNotifyMinRipSeconds,
		NotifyQueueMinItems:         defaultNotifyQueueMinItems,
		NotifyDedupWindowSeconds:    defaultNotifyDedupWindowSeconds,
		PresetDeciderBaseURL:        defaultPresetDeciderBaseURL,
		PresetDeciderModel:          defaultPresetDeciderModel,
		PresetDeciderReferer:        defaultPresetDeciderReferer,
		PresetDeciderTitle:          defaultPresetDeciderTitle,
	}
}

// DefaultConfigPath returns the absolute path to the default configuration file location.
func DefaultConfigPath() (string, error) {
	return expandPath("~/.config/spindle/config.toml")
}

// Load locates, parses, and validates a configuration file. The returned config has all
// path fields expanded and normalized.
func Load(path string) (*Config, string, bool, error) {
	cfg := Default()

	resolvedPath, exists, err := resolveConfigPath(path)
	if err != nil {
		return nil, "", false, err
	}

	if exists {
		file, err := os.Open(resolvedPath)
		if err != nil {
			return nil, "", false, fmt.Errorf("open config: %w", err)
		}
		defer file.Close()

		decoder := toml.NewDecoder(file)
		if err := decoder.Decode(&cfg); err != nil {
			return nil, "", false, fmt.Errorf("parse config: %w", err)
		}
	}

	if err := cfg.normalize(); err != nil {
		return nil, "", false, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, "", false, err
	}

	return &cfg, resolvedPath, exists, nil
}

func resolveConfigPath(path string) (string, bool, error) {
	if path != "" {
		expanded, err := expandPath(path)
		if err != nil {
			return "", false, err
		}
		_, err = os.Stat(expanded)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return expanded, false, nil
			}
			return "", false, fmt.Errorf("stat config: %w", err)
		}
		return expanded, true, nil
	}

	defaultPath, err := expandPath("~/.config/spindle/config.toml")
	if err != nil {
		return "", false, err
	}

	projectPath, err := filepath.Abs("spindle.toml")
	if err != nil {
		return "", false, err
	}

	if info, err := os.Stat(defaultPath); err == nil && !info.IsDir() {
		return defaultPath, true, nil
	}
	if info, err := os.Stat(projectPath); err == nil && !info.IsDir() {
		return projectPath, true, nil
	}

	return defaultPath, false, nil
}

func (c *Config) normalize() error {
	var err error
	if c.StagingDir, err = expandPath(c.StagingDir); err != nil {
		return fmt.Errorf("staging_dir: %w", err)
	}
	if c.LibraryDir, err = expandPath(c.LibraryDir); err != nil {
		return fmt.Errorf("library_dir: %w", err)
	}
	if c.LogDir, err = expandPath(c.LogDir); err != nil {
		return fmt.Errorf("log_dir: %w", err)
	}
	if c.LogRetentionDays < 0 {
		return fmt.Errorf("log_retention_days must be >= 0")
	}
	if strings.TrimSpace(c.OpenSubtitlesCacheDir) == "" {
		c.OpenSubtitlesCacheDir = defaultOpenSubtitlesCacheDir
	}
	if c.OpenSubtitlesCacheDir, err = expandPath(c.OpenSubtitlesCacheDir); err != nil {
		return fmt.Errorf("opensubtitles_cache_dir: %w", err)
	}
	if strings.TrimSpace(c.WhisperXCacheDir) == "" {
		c.WhisperXCacheDir = defaultWhisperXCacheDir
	}
	if c.WhisperXCacheDir, err = expandPath(c.WhisperXCacheDir); err != nil {
		return fmt.Errorf("whisperx_cache_dir: %w", err)
	}
	if strings.TrimSpace(c.RipCacheDir) == "" {
		c.RipCacheDir = defaultRipCacheDir()
	}
	if c.RipCacheDir, err = expandPath(c.RipCacheDir); err != nil {
		return fmt.Errorf("rip_cache_dir: %w", err)
	}
	if c.RipCacheMaxGiB <= 0 {
		c.RipCacheMaxGiB = defaultRipCacheMaxGiB
	}
	if c.ReviewDir, err = expandPath(c.ReviewDir); err != nil {
		return fmt.Errorf("review_dir: %w", err)
	}
	if c.PlexAuthPath, err = expandPath(c.PlexAuthPath); err != nil {
		return fmt.Errorf("plex_auth_path: %w", err)
	}
	if c.KeyDBPath, err = expandPath(c.KeyDBPath); err != nil {
		return fmt.Errorf("keydb_path: %w", err)
	}
	if strings.TrimSpace(c.IdentificationOverridesPath) == "" {
		c.IdentificationOverridesPath = defaultIdentificationOverridesPath
	}
	if c.IdentificationOverridesPath, err = expandPath(c.IdentificationOverridesPath); err != nil {
		return fmt.Errorf("identification_overrides_path: %w", err)
	}
	if strings.TrimSpace(c.KeyDBDownloadURL) == "" {
		c.KeyDBDownloadURL = defaultKeyDBDownloadURL
	}
	c.KeyDBDownloadURL = strings.TrimSpace(c.KeyDBDownloadURL)
	if c.KeyDBDownloadTimeout <= 0 {
		c.KeyDBDownloadTimeout = defaultKeyDBDownloadTimeout
	}
	c.APIBind = strings.TrimSpace(c.APIBind)
	if c.APIBind == "" {
		c.APIBind = defaultAPIBind
	}

	c.LogFormat = strings.ToLower(strings.TrimSpace(c.LogFormat))
	switch c.LogFormat {
	case "", "console":
		c.LogFormat = "console"
	case "json":
	default:
		if c.LogFormat != "json" {
			return fmt.Errorf("log_format: unsupported value %q", c.LogFormat)
		}
	}

	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	if c.LogLevel == "" {
		c.LogLevel = defaultLogLevel
	}

	if c.TMDBAPIKey == "" {
		if value, ok := os.LookupEnv("TMDB_API_KEY"); ok {
			c.TMDBAPIKey = value
		}
	}

	c.WhisperXVADMethod = strings.ToLower(strings.TrimSpace(c.WhisperXVADMethod))
	if c.WhisperXVADMethod == "" {
		c.WhisperXVADMethod = "silero"
	}
	c.WhisperXHuggingFaceToken = strings.TrimSpace(c.WhisperXHuggingFaceToken)
	if c.WhisperXHuggingFaceToken == "" {
		if value, ok := os.LookupEnv("HUGGING_FACE_HUB_TOKEN"); ok {
			c.WhisperXHuggingFaceToken = strings.TrimSpace(value)
		} else if value, ok := os.LookupEnv("HF_TOKEN"); ok {
			c.WhisperXHuggingFaceToken = strings.TrimSpace(value)
		}
	}

	c.OpenSubtitlesAPIKey = strings.TrimSpace(c.OpenSubtitlesAPIKey)
	if c.OpenSubtitlesAPIKey == "" {
		if value, ok := os.LookupEnv("OPENSUBTITLES_API_KEY"); ok {
			c.OpenSubtitlesAPIKey = strings.TrimSpace(value)
		}
	}
	c.OpenSubtitlesUserAgent = strings.TrimSpace(c.OpenSubtitlesUserAgent)
	if c.OpenSubtitlesUserAgent == "" {
		c.OpenSubtitlesUserAgent = defaultOpenSubtitlesUserAgent
	}
	c.OpenSubtitlesUserToken = strings.TrimSpace(c.OpenSubtitlesUserToken)
	if c.OpenSubtitlesUserToken == "" {
		if value, ok := os.LookupEnv("OPENSUBTITLES_USER_TOKEN"); ok {
			c.OpenSubtitlesUserToken = strings.TrimSpace(value)
		}
	}
	if len(c.OpenSubtitlesLanguages) == 0 {
		c.OpenSubtitlesLanguages = []string{"en"}
	} else {
		langs := make([]string, 0, len(c.OpenSubtitlesLanguages))
		seen := make(map[string]struct{}, len(c.OpenSubtitlesLanguages))
		for _, lang := range c.OpenSubtitlesLanguages {
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
		c.OpenSubtitlesLanguages = langs
	}

	if !c.PresetDeciderEnabled && c.DeepSeekPresetDeciderEnabled {
		c.PresetDeciderEnabled = true
	}
	c.PresetDeciderBaseURL = strings.TrimSpace(c.PresetDeciderBaseURL)
	if c.PresetDeciderBaseURL == "" {
		c.PresetDeciderBaseURL = defaultPresetDeciderBaseURL
	}
	c.PresetDeciderModel = strings.TrimSpace(c.PresetDeciderModel)
	if c.PresetDeciderModel == "" {
		c.PresetDeciderModel = defaultPresetDeciderModel
	}
	c.PresetDeciderReferer = strings.TrimSpace(c.PresetDeciderReferer)
	if c.PresetDeciderReferer == "" {
		c.PresetDeciderReferer = defaultPresetDeciderReferer
	}
	c.PresetDeciderTitle = strings.TrimSpace(c.PresetDeciderTitle)
	if c.PresetDeciderTitle == "" {
		c.PresetDeciderTitle = defaultPresetDeciderTitle
	}
	c.PresetDeciderAPIKey = strings.TrimSpace(c.PresetDeciderAPIKey)
	if c.PresetDeciderAPIKey == "" {
		if value, ok := os.LookupEnv("PRESET_DECIDER_API_KEY"); ok {
			c.PresetDeciderAPIKey = strings.TrimSpace(value)
		} else if value, ok := os.LookupEnv("OPENROUTER_API_KEY"); ok {
			c.PresetDeciderAPIKey = strings.TrimSpace(value)
		} else if strings.TrimSpace(c.DeepSeekAPIKey) != "" {
			c.PresetDeciderAPIKey = strings.TrimSpace(c.DeepSeekAPIKey)
		} else if value, ok := os.LookupEnv("DEEPSEEK_API_KEY"); ok {
			c.PresetDeciderAPIKey = strings.TrimSpace(value)
		}
	}

	c.DeepSeekAPIKey = strings.TrimSpace(c.DeepSeekAPIKey)
	if c.DeepSeekAPIKey == "" {
		if value, ok := os.LookupEnv("DEEPSEEK_API_KEY"); ok {
			c.DeepSeekAPIKey = strings.TrimSpace(value)
		}
	}

	c.TMDBBaseURL = strings.TrimSpace(c.TMDBBaseURL)
	if c.TMDBBaseURL == "" {
		c.TMDBBaseURL = defaultTMDBBaseURL
	}

	return nil
}

// Validate ensures the configuration is usable.
func (c *Config) Validate() error {
	if c.TMDBAPIKey == "" {
		defaultPath, err := DefaultConfigPath()
		if err != nil {
			defaultPath = "~/.config/spindle/config.toml"
		}
		return fmt.Errorf("tmdb_api_key is required. Set TMDB_API_KEY env var or edit %s (create with 'spindle config init')", defaultPath)
	}
	if c.MoviesDir == "" {
		return errors.New("movies_dir must be set")
	}
	if c.TVDir == "" {
		return errors.New("tv_dir must be set")
	}
	if c.MoviesLibrary == "" {
		return errors.New("movies_library must be set")
	}
	if c.TVLibrary == "" {
		return errors.New("tv_library must be set")
	}
	if err := ensurePositiveMap(map[string]int{
		"makemkv_rip_timeout":  c.MakeMKVRipTimeout,
		"makemkv_info_timeout": c.MakeMKVInfoTimeout,
		"ntfy_request_timeout": c.NtfyRequestTimeout,
		"disc_monitor_timeout": c.DiscMonitorTimeout,
		"queue_poll_interval":  c.QueuePollInterval,
		"error_retry_interval": c.ErrorRetryInterval,
	}); err != nil {
		return err
	}
	if c.WorkflowHeartbeatInterval <= 0 {
		return errors.New("workflow_heartbeat_interval must be positive")
	}
	if c.WorkflowHeartbeatTimeout <= 0 {
		return errors.New("workflow_heartbeat_timeout must be positive")
	}
	if c.WorkflowHeartbeatTimeout <= c.WorkflowHeartbeatInterval {
		return errors.New("workflow_heartbeat_timeout must be greater than workflow_heartbeat_interval")
	}
	if c.TMDBConfidenceThreshold < 0 || c.TMDBConfidenceThreshold > 1 {
		return errors.New("tmdb_confidence_threshold must be between 0 and 1")
	}
	if strings.TrimSpace(c.KeyDBDownloadURL) == "" {
		return errors.New("keydb_download_url must be set")
	}
	if c.KeyDBDownloadTimeout <= 0 {
		return errors.New("keydb_download_timeout must be positive (seconds)")
	}
	if c.OpenSubtitlesEnabled {
		if strings.TrimSpace(c.OpenSubtitlesAPIKey) == "" {
			return errors.New("opensubtitles_api_key must be set when opensubtitles_enabled is true")
		}
		if strings.TrimSpace(c.OpenSubtitlesUserAgent) == "" {
			return errors.New("opensubtitles_user_agent must be set when opensubtitles_enabled is true")
		}
		if len(c.OpenSubtitlesLanguages) == 0 {
			return errors.New("opensubtitles_languages must include at least one language when opensubtitles_enabled is true")
		}
	}
	if c.RipCacheEnabled {
		if strings.TrimSpace(c.RipCacheDir) == "" {
			return errors.New("rip_cache_dir must be set when rip_cache_enabled is true")
		}
		if c.RipCacheMaxGiB <= 0 {
			return errors.New("rip_cache_max_gib must be positive when rip_cache_enabled is true")
		}
	}
	if c.PresetDeciderEnabled && strings.TrimSpace(c.PresetDeciderAPIKey) == "" {
		return errors.New("preset_decider_api_key must be set when preset_decider_enabled is true (or set OPENROUTER_API_KEY)")
	}
	if c.NotifyMinRipSeconds < 0 {
		return errors.New("notify_min_rip_seconds must be >= 0")
	}
	if c.NotifyQueueMinItems < 1 {
		return errors.New("notify_queue_min_items must be >= 1")
	}
	if c.NotifyDedupWindowSeconds < 0 {
		return errors.New("notify_dedup_window_seconds must be >= 0")
	}
	return nil
}

// EnsureDirectories creates required directories for daemon operation.
func (c *Config) EnsureDirectories() error {
	for _, dir := range []string{c.StagingDir, c.LibraryDir, c.LogDir, c.ReviewDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	if c.RipCacheEnabled && strings.TrimSpace(c.RipCacheDir) != "" {
		if err := os.MkdirAll(c.RipCacheDir, 0o755); err != nil {
			return fmt.Errorf("create rip cache directory %q: %w", c.RipCacheDir, err)
		}
	}
	if strings.TrimSpace(c.PlexAuthPath) != "" {
		authDir := filepath.Dir(c.PlexAuthPath)
		if err := os.MkdirAll(authDir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", authDir, err)
		}
	}
	return nil
}

// MakemkvBinary returns the MakeMKV executable name.
func (c *Config) MakemkvBinary() string {
	return "makemkvcon"
}

// DraptoBinary returns the Drapto executable name.
func (c *Config) DraptoBinary() string {
	return "drapto"
}

// FFprobeBinary returns the ffprobe executable name used for media validation.
func (c *Config) FFprobeBinary() string {
	return "ffprobe"
}

func expandPath(pathValue string) (string, error) {
	if pathValue == "" {
		return pathValue, nil
	}
	if strings.HasPrefix(pathValue, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if pathValue == "~" {
			pathValue = home
		} else if len(pathValue) > 1 && (pathValue[1] == '/' || pathValue[1] == '\\') {
			pathValue = filepath.Join(home, pathValue[2:])
		}
	}
	cleaned := filepath.Clean(pathValue)
	absolute, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", cleaned, err)
	}
	return absolute, nil
}

// ExpandPath exposes the repository path expansion rules for other packages.
func ExpandPath(pathValue string) (string, error) {
	return expandPath(pathValue)
}

func defaultRipCacheDir() string {
	if base, ok := os.LookupEnv("XDG_CACHE_HOME"); ok && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "spindle", "rips")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.cache/spindle/rips"
	}
	return filepath.Join(home, ".cache", "spindle", "rips")
}

// CreateSample writes a sample configuration file to the specified location.
func CreateSample(path string) error {
	sample := sampleConfig

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}

	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		return fmt.Errorf("write sample config: %w", err)
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
