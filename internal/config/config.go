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
	APIToken              string `toml:"api_token"`
}

// TMDB contains configuration for The Movie Database API.
type TMDB struct {
	APIKey   string `toml:"api_key"`
	BaseURL  string `toml:"base_url"`
	Language string `toml:"language"`
}

// Jellyfin contains configuration for Jellyfin Media Server integration.
type Jellyfin struct {
	Enabled bool   `toml:"enabled"`
	URL     string `toml:"url"`
	APIKey  string `toml:"api_key"`
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
	Validation         bool   `toml:"validation"`
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
	MuxIntoMKV             bool     `toml:"mux_into_mkv"`
	WhisperXModel          string   `toml:"whisperx_model"`
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

// DiscIDCache contains configuration for the disc ID to TMDB ID cache.
type DiscIDCache struct {
	Enabled bool   `toml:"enabled"` // Default: false
	Path    string `toml:"path"`    // Default: ~/.cache/spindle/discid_cache.json
}

// MakeMKV contains configuration for disc ripping.
type MakeMKV struct {
	OpticalDrive         string `toml:"optical_drive"`
	RipTimeout           int    `toml:"rip_timeout"`
	InfoTimeout          int    `toml:"info_timeout"`
	KeyDBPath            string `toml:"keydb_path"`
	KeyDBDownloadURL     string `toml:"keydb_download_url"`
	KeyDBDownloadTimeout int    `toml:"keydb_download_timeout"`
}

// LLM contains shared LLM connection settings used by multiple features.
type LLM struct {
	APIKey         string `toml:"api_key"`
	BaseURL        string `toml:"base_url"`
	Model          string `toml:"model"`
	Referer        string `toml:"referer"`
	Title          string `toml:"title"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
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
	Format         string            `toml:"format"`
	Level          string            `toml:"level"`
	RetentionDays  int               `toml:"retention_days"`
	StageOverrides map[string]string `toml:"stage_overrides"`
}

// Validation contains configuration for pipeline validation checks.
type Validation struct {
	// Encoding validation
	EnforceDraptoValidation bool `toml:"enforce_drapto_validation"`

	// Identification validation
	MinVoteCountExactMatch int `toml:"min_vote_count_exact_match"`
}

// Commentary contains configuration for commentary track detection.
type Commentary struct {
	// Enabled controls whether commentary detection runs during audio analysis.
	Enabled bool `toml:"enabled"`
	// WhisperXModel is the model to use for transcription (e.g., "large-v3-turbo").
	// If empty, defaults to the subtitles model or "large-v3-turbo".
	WhisperXModel string `toml:"whisperx_model"`
	// SimilarityThreshold is the cosine similarity above which a track is considered
	// a stereo downmix of the primary audio (not commentary). Default: 0.92
	SimilarityThreshold float64 `toml:"similarity_threshold"`
	// ConfidenceThreshold is the LLM confidence required to classify a track as
	// commentary. Default: 0.80
	ConfidenceThreshold float64 `toml:"confidence_threshold"`
	// LLM settings - if not set, falls back to [llm] settings
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Model   string `toml:"model"`
}

// Config encapsulates all configuration values for Spindle.
//
// Configuration sections by subsystem:
//   - Paths: directories and API bind address
//   - TMDB: disc identification via The Movie Database
//   - Jellyfin: media server library refresh integration
//   - Library: output directory structure (movies/tv subdirs)
//   - Notifications: ntfy push notification settings
//   - Subtitles: OpenSubtitles + WhisperX configuration
//   - RipCache: cached raw rips for re-encoding
//   - DiscIDCache: disc ID to TMDB ID mapping cache
//   - MakeMKV: disc ripping settings and keydb
//   - LLM: shared LLM connection settings for features that need AI
//   - Commentary: commentary track detection via audio analysis
//   - Workflow: daemon polling intervals and timeouts
//   - Logging: log format, level, and retention
//   - Validation: pipeline validation checks and thresholds
type Config struct {
	Paths         Paths         `toml:"paths"`
	TMDB          TMDB          `toml:"tmdb"`
	Jellyfin      Jellyfin      `toml:"jellyfin"`
	Library       Library       `toml:"library"`
	Notifications Notifications `toml:"notifications"`
	Subtitles     Subtitles     `toml:"subtitles"`
	RipCache      RipCache      `toml:"rip_cache"`
	DiscIDCache   DiscIDCache   `toml:"disc_id_cache"`
	MakeMKV       MakeMKV       `toml:"makemkv"`
	LLM           LLM           `toml:"llm"`
	Commentary    Commentary    `toml:"commentary"`
	Workflow      Workflow      `toml:"workflow"`
	Logging       Logging       `toml:"logging"`
	Validation    Validation    `toml:"validation"`
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

// EnsureDirectories creates required directories for daemon operation.
// LibraryDir is created on a best-effort basis so the daemon can run when
// external storage is temporarily unavailable.
func (c *Config) EnsureDirectories() error {
	for _, dir := range []string{c.Paths.StagingDir, c.Paths.LogDir, c.Paths.ReviewDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	if strings.TrimSpace(c.Paths.LibraryDir) != "" {
		// Best-effort to avoid failing config load when storage is offline.
		_ = os.MkdirAll(c.Paths.LibraryDir, 0o755)
	}
	if c.RipCache.Enabled && strings.TrimSpace(c.RipCache.Dir) != "" {
		if err := os.MkdirAll(c.RipCache.Dir, 0o755); err != nil {
			return fmt.Errorf("create rip cache directory %q: %w", c.RipCache.Dir, err)
		}
	}
	return nil
}

// MakemkvBinary returns the MakeMKV executable name.
func (c *Config) MakemkvBinary() string {
	return "makemkvcon"
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

// LLMConfig contains common LLM settings used across features.
type LLMConfig struct {
	APIKey         string
	BaseURL        string
	Model          string
	Referer        string
	Title          string
	TimeoutSeconds int
}

// GetLLM returns the shared LLM connection settings.
func (c *Config) GetLLM() LLMConfig {
	return LLMConfig{
		APIKey:         strings.TrimSpace(c.LLM.APIKey),
		BaseURL:        strings.TrimSpace(c.LLM.BaseURL),
		Model:          strings.TrimSpace(c.LLM.Model),
		Referer:        strings.TrimSpace(c.LLM.Referer),
		Title:          strings.TrimSpace(c.LLM.Title),
		TimeoutSeconds: c.LLM.TimeoutSeconds,
	}
}

// CommentaryLLM returns the LLM settings for commentary detection.
// Falls back to [llm] settings when not explicitly configured.
func (c *Config) CommentaryLLM() LLMConfig {
	cfg := LLMConfig{
		APIKey:  strings.TrimSpace(c.Commentary.APIKey),
		BaseURL: strings.TrimSpace(c.Commentary.BaseURL),
		Model:   strings.TrimSpace(c.Commentary.Model),
		Referer: strings.TrimSpace(c.LLM.Referer),
		Title:   defaultCommentaryTitle,
	}
	// Fall back to [llm] settings for connection details
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(c.LLM.APIKey)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = strings.TrimSpace(c.LLM.BaseURL)
	}
	if cfg.Model == "" {
		cfg.Model = strings.TrimSpace(c.LLM.Model)
	}
	return cfg
}
