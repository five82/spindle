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

// Paths contains directory and bind address configuration.
type Paths struct {
	StagingDir            string `toml:"staging_dir"`
	LibraryDir            string `toml:"library_dir"`
	LogDir                string `toml:"log_dir"`
	ReviewDir             string `toml:"review_dir"`
	OpenSubtitlesCacheDir string `toml:"opensubtitles_cache_dir"`
	WhisperXCacheDir      string `toml:"whisperx_cache_dir"`
	APIBind               string `toml:"api_bind"`
}

// TMDB contains configuration for The Movie Database API.
type TMDB struct {
	APIKey              string  `toml:"api_key"`
	BaseURL             string  `toml:"base_url"`
	Language            string  `toml:"language"`
	ConfidenceThreshold float64 `toml:"confidence_threshold"`
}

// Plex contains configuration for Plex Media Server integration.
type Plex struct {
	Enabled       bool   `toml:"enabled"`
	URL           string `toml:"url"`
	AuthPath      string `toml:"auth_path"`
	MoviesLibrary string `toml:"movies_library"`
	TVLibrary     string `toml:"tv_library"`
}

// Library contains configuration for the media library structure.
type Library struct {
	MoviesDir         string `toml:"movies_dir"`
	TVDir             string `toml:"tv_dir"`
	OverwriteExisting bool   `toml:"overwrite_existing"`
}

// Notifications contains configuration for ntfy push notifications.
type Notifications struct {
	NtfyTopic          string `toml:"ntfy_topic"`
	RequestTimeout     int    `toml:"request_timeout"`
	Identification     bool   `toml:"identification"`
	Rip                bool   `toml:"rip"`
	Encoding           bool   `toml:"encoding"`
	Organization       bool   `toml:"organization"`
	Queue              bool   `toml:"queue"`
	Review             bool   `toml:"review"`
	Errors             bool   `toml:"errors"`
	MinRipSeconds      int    `toml:"min_rip_seconds"`
	QueueMinItems      int    `toml:"queue_min_items"`
	DedupWindowSeconds int    `toml:"dedup_window_seconds"`
}

// Subtitles contains configuration for subtitle generation and retrieval.
type Subtitles struct {
	Enabled                bool     `toml:"enabled"`
	WhisperXCUDAEnabled    bool     `toml:"whisperx_cuda_enabled"`
	WhisperXVADMethod      string   `toml:"whisperx_vad_method"`
	WhisperXHuggingFace    string   `toml:"whisperx_hf_token"`
	OpenSubtitlesEnabled   bool     `toml:"opensubtitles_enabled"`
	OpenSubtitlesAPIKey    string   `toml:"opensubtitles_api_key"`
	OpenSubtitlesUserAgent string   `toml:"opensubtitles_user_agent"`
	OpenSubtitlesUserToken string   `toml:"opensubtitles_user_token"`
	OpenSubtitlesLanguages []string `toml:"opensubtitles_languages"`
}

// RipCache contains configuration for the rip cache.
type RipCache struct {
	Enabled bool   `toml:"enabled"`
	Dir     string `toml:"dir"`
	MaxGiB  int    `toml:"max_gib"`
}

// MakeMKV contains configuration for disc ripping.
type MakeMKV struct {
	OpticalDrive                string `toml:"optical_drive"`
	RipTimeout                  int    `toml:"rip_timeout"`
	InfoTimeout                 int    `toml:"info_timeout"`
	KeyDBPath                   string `toml:"keydb_path"`
	KeyDBDownloadURL            string `toml:"keydb_download_url"`
	KeyDBDownloadTimeout        int    `toml:"keydb_download_timeout"`
	IdentificationOverridesPath string `toml:"identification_overrides_path"`
}

// PresetDecider contains configuration for LLM-based encoding preset selection.
type PresetDecider struct {
	Enabled bool   `toml:"enabled"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Model   string `toml:"model"`
	Referer string `toml:"referer"`
	Title   string `toml:"title"`
}

// CommentaryDetection contains configuration for LLM-based commentary track detection.
type CommentaryDetection struct {
	Enabled bool   `toml:"enabled"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Model   string `toml:"model"`
	Referer string `toml:"referer"`
	Title   string `toml:"title"`
}

// Workflow contains configuration for daemon timing and intervals.
type Workflow struct {
	QueuePollInterval  int `toml:"queue_poll_interval"`
	ErrorRetryInterval int `toml:"error_retry_interval"`
	HeartbeatInterval  int `toml:"heartbeat_interval"`
	HeartbeatTimeout   int `toml:"heartbeat_timeout"`
	DiscMonitorTimeout int `toml:"disc_monitor_timeout"`
}

// Logging contains configuration for log output.
type Logging struct {
	Format        string `toml:"format"`
	Level         string `toml:"level"`
	RetentionDays int    `toml:"retention_days"`
}

// Config encapsulates all configuration values for Spindle.
type Config struct {
	Paths               Paths               `toml:"paths"`
	TMDB                TMDB                `toml:"tmdb"`
	Plex                Plex                `toml:"plex"`
	Library             Library             `toml:"library"`
	Notifications       Notifications       `toml:"notifications"`
	Subtitles           Subtitles           `toml:"subtitles"`
	RipCache            RipCache            `toml:"rip_cache"`
	MakeMKV             MakeMKV             `toml:"makemkv"`
	PresetDecider       PresetDecider       `toml:"preset_decider"`
	CommentaryDetection CommentaryDetection `toml:"commentary_detection"`
	Workflow            Workflow            `toml:"workflow"`
	Logging             Logging             `toml:"logging"`
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
	defaultCommentaryDetectionTitle    = "Spindle Commentary Detector"
)

// Default returns a Config populated with repository defaults.
func Default() Config {
	return Config{
		Paths: Paths{
			StagingDir:            defaultStagingDir,
			LibraryDir:            defaultLibraryDir,
			LogDir:                defaultLogDir,
			ReviewDir:             defaultReviewDir,
			OpenSubtitlesCacheDir: defaultOpenSubtitlesCacheDir,
			WhisperXCacheDir:      defaultWhisperXCacheDir,
			APIBind:               defaultAPIBind,
		},
		TMDB: TMDB{
			Language:            defaultTMDBLanguage,
			BaseURL:             defaultTMDBBaseURL,
			ConfidenceThreshold: 0.8,
		},
		Plex: Plex{
			Enabled:       true,
			AuthPath:      defaultPlexAuthPath,
			MoviesLibrary: "Movies",
			TVLibrary:     "TV Shows",
		},
		Library: Library{
			MoviesDir: defaultMoviesDir,
			TVDir:     defaultTVDir,
		},
		Notifications: Notifications{
			RequestTimeout:     10,
			Identification:     true,
			Rip:                true,
			Encoding:           true,
			Organization:       true,
			Queue:              true,
			Review:             true,
			Errors:             true,
			MinRipSeconds:      defaultNotifyMinRipSeconds,
			QueueMinItems:      defaultNotifyQueueMinItems,
			DedupWindowSeconds: defaultNotifyDedupWindowSeconds,
		},
		Subtitles: Subtitles{
			WhisperXVADMethod:      "silero",
			OpenSubtitlesLanguages: []string{"en"},
			OpenSubtitlesUserAgent: defaultOpenSubtitlesUserAgent,
		},
		RipCache: RipCache{
			Dir:    defaultRipCacheDir(),
			MaxGiB: defaultRipCacheMaxGiB,
		},
		MakeMKV: MakeMKV{
			OpticalDrive:                defaultOpticalDrive,
			RipTimeout:                  3600,
			InfoTimeout:                 300,
			KeyDBPath:                   defaultKeyDBPath,
			KeyDBDownloadURL:            defaultKeyDBDownloadURL,
			KeyDBDownloadTimeout:        defaultKeyDBDownloadTimeout,
			IdentificationOverridesPath: defaultIdentificationOverridesPath,
		},
		PresetDecider: PresetDecider{
			BaseURL: defaultPresetDeciderBaseURL,
			Model:   defaultPresetDeciderModel,
			Referer: defaultPresetDeciderReferer,
			Title:   defaultPresetDeciderTitle,
		},
		CommentaryDetection: CommentaryDetection{
			BaseURL: defaultPresetDeciderBaseURL,
			Model:   defaultPresetDeciderModel,
			Referer: defaultPresetDeciderReferer,
			Title:   defaultCommentaryDetectionTitle,
		},
		Workflow: Workflow{
			QueuePollInterval:  5,
			ErrorRetryInterval: 10,
			HeartbeatInterval:  defaultWorkflowHeartbeatInterval,
			HeartbeatTimeout:   defaultWorkflowHeartbeatTimeout,
			DiscMonitorTimeout: 5,
		},
		Logging: Logging{
			Format:        defaultLogFormat,
			Level:         defaultLogLevel,
			RetentionDays: defaultLogRetentionDays,
		},
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
	if err := c.normalizePaths(); err != nil {
		return err
	}
	if err := c.normalizeTMDB(); err != nil {
		return err
	}
	if err := c.normalizePlex(); err != nil {
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
	if err := c.normalizeCommentaryDetection(); err != nil {
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

func (c *Config) normalizePlex() error {
	var err error
	if c.Plex.AuthPath, err = expandPath(c.Plex.AuthPath); err != nil {
		return fmt.Errorf("plex.auth_path: %w", err)
	}
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

func (c *Config) normalizeCommentaryDetection() error {
	c.CommentaryDetection.BaseURL = strings.TrimSpace(c.CommentaryDetection.BaseURL)
	if c.CommentaryDetection.BaseURL == "" {
		c.CommentaryDetection.BaseURL = c.PresetDecider.BaseURL
	}
	if c.CommentaryDetection.BaseURL == "" {
		c.CommentaryDetection.BaseURL = defaultPresetDeciderBaseURL
	}
	c.CommentaryDetection.Model = strings.TrimSpace(c.CommentaryDetection.Model)
	if c.CommentaryDetection.Model == "" {
		c.CommentaryDetection.Model = c.PresetDecider.Model
	}
	if c.CommentaryDetection.Model == "" {
		c.CommentaryDetection.Model = defaultPresetDeciderModel
	}
	c.CommentaryDetection.Referer = strings.TrimSpace(c.CommentaryDetection.Referer)
	if c.CommentaryDetection.Referer == "" {
		c.CommentaryDetection.Referer = c.PresetDecider.Referer
	}
	if c.CommentaryDetection.Referer == "" {
		c.CommentaryDetection.Referer = defaultPresetDeciderReferer
	}
	c.CommentaryDetection.Title = strings.TrimSpace(c.CommentaryDetection.Title)
	if c.CommentaryDetection.Title == "" {
		c.CommentaryDetection.Title = defaultCommentaryDetectionTitle
	}
	c.CommentaryDetection.APIKey = strings.TrimSpace(c.CommentaryDetection.APIKey)
	if c.CommentaryDetection.APIKey == "" {
		c.CommentaryDetection.APIKey = c.PresetDecider.APIKey
	}
	if c.CommentaryDetection.APIKey == "" {
		if value, ok := os.LookupEnv("COMMENTARY_DETECTION_API_KEY"); ok {
			c.CommentaryDetection.APIKey = strings.TrimSpace(value)
		} else if value, ok := os.LookupEnv("OPENROUTER_API_KEY"); ok {
			c.CommentaryDetection.APIKey = strings.TrimSpace(value)
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

// Validate ensures the configuration is usable.
func (c *Config) Validate() error {
	if err := c.validateTMDB(); err != nil {
		return err
	}
	if err := c.validateLibrary(); err != nil {
		return err
	}
	if err := c.validatePlex(); err != nil {
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
	if err := c.validateCommentaryDetection(); err != nil {
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
	if c.TMDB.ConfidenceThreshold < 0 || c.TMDB.ConfidenceThreshold > 1 {
		return errors.New("tmdb.confidence_threshold must be between 0 and 1")
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

func (c *Config) validatePlex() error {
	if c.Plex.MoviesLibrary == "" {
		return errors.New("plex.movies_library must be set")
	}
	if c.Plex.TVLibrary == "" {
		return errors.New("plex.tv_library must be set")
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

func (c *Config) validateCommentaryDetection() error {
	if c.CommentaryDetection.Enabled && strings.TrimSpace(c.CommentaryDetection.APIKey) == "" {
		return errors.New("commentary_detection.api_key must be set when commentary_detection.enabled is true (or set OPENROUTER_API_KEY)")
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

// EnsureDirectories creates required directories for daemon operation.
func (c *Config) EnsureDirectories() error {
	for _, dir := range []string{c.Paths.StagingDir, c.Paths.LibraryDir, c.Paths.LogDir, c.Paths.ReviewDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	if c.RipCache.Enabled && strings.TrimSpace(c.RipCache.Dir) != "" {
		if err := os.MkdirAll(c.RipCache.Dir, 0o755); err != nil {
			return fmt.Errorf("create rip cache directory %q: %w", c.RipCache.Dir, err)
		}
	}
	if strings.TrimSpace(c.Plex.AuthPath) != "" {
		authDir := filepath.Dir(c.Plex.AuthPath)
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
