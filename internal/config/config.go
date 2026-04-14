package config

import (
	"os"
	"path/filepath"
	"time"
)

// Config holds all Spindle configuration sections.
type Config struct {
	// SourcePath is the resolved filesystem path of the config file that was loaded.
	// Empty when using defaults only (no config file found).
	// Used by ReloadEncoding to re-read encoding parameters from disk.
	SourcePath string `toml:"-"`

	Paths         PathsConfig         `toml:"paths"`
	API           APIConfig           `toml:"api"`
	TMDB          TMDBConfig          `toml:"tmdb"`
	Jellyfin      JellyfinConfig      `toml:"jellyfin"`
	Library       LibraryConfig       `toml:"library"`
	Notifications NotificationsConfig `toml:"notifications"`
	Subtitles     SubtitlesConfig     `toml:"subtitles"`
	Transcription TranscriptionConfig `toml:"transcription"`
	RipCache      RipCacheConfig      `toml:"rip_cache"`
	DiscIDCache   DiscIDCacheConfig   `toml:"disc_id_cache"`
	MakeMKV       MakeMKVConfig       `toml:"makemkv"`
	Encoding      EncodingConfig      `toml:"encoding"`
	LLM           LLMConfig           `toml:"llm"`
	Commentary    CommentaryConfig    `toml:"commentary"`
	ContentID     ContentIDConfig     `toml:"content_id"`
	Logging       LoggingConfig       `toml:"logging"`
}

// PathsConfig defines filesystem paths for staging, library, state, and review.
type PathsConfig struct {
	StagingDir string `toml:"staging_dir"`
	LibraryDir string `toml:"library_dir"`
	StateDir   string `toml:"state_dir"`
	ReviewDir  string `toml:"review_dir"`
}

// APIConfig defines the HTTP API server settings.
type APIConfig struct {
	Bind  string `toml:"bind"`
	Token string `toml:"token"`
}

// TMDBConfig defines The Movie Database API settings.
type TMDBConfig struct {
	APIKey   string `toml:"api_key"`
	BaseURL  string `toml:"base_url"`
	Language string `toml:"language"`
}

// JellyfinConfig defines Jellyfin server integration settings.
type JellyfinConfig struct {
	Enabled bool   `toml:"enabled"`
	URL     string `toml:"url"`
	APIKey  string `toml:"api_key"`
}

// LibraryConfig defines media library directory structure settings.
type LibraryConfig struct {
	MoviesDir         string `toml:"movies_dir"`
	TVDir             string `toml:"tv_dir"`
	OverwriteExisting bool   `toml:"overwrite_existing"`
}

// NotificationsConfig defines ntfy notification settings.
type NotificationsConfig struct {
	NtfyTopic      string `toml:"ntfy_topic"`
	RequestTimeout int    `toml:"request_timeout"`
}

// SubtitlesConfig defines subtitle generation pipeline settings.
type SubtitlesConfig struct {
	Enabled                bool     `toml:"enabled"`
	MuxIntoMKV             bool     `toml:"mux_into_mkv"`
	OpenSubtitlesEnabled   bool     `toml:"opensubtitles_enabled"`
	OpenSubtitlesAPIKey    string   `toml:"opensubtitles_api_key"`
	OpenSubtitlesUserAgent string   `toml:"opensubtitles_user_agent"`
	OpenSubtitlesUserToken string   `toml:"opensubtitles_user_token"`
	OpenSubtitlesLanguages []string `toml:"opensubtitles_languages"`
}

// TranscriptionConfig defines the shared ASR/forced-alignment runtime.
type TranscriptionConfig struct {
	ASRModel              string `toml:"asr_model"`
	ForcedAlignerModel    string `toml:"forced_aligner_model"`
	Device                string `toml:"device"`
	DType                 string `toml:"dtype"`
	UseFlashAttention     bool   `toml:"use_flash_attention"`
	MaxInferenceBatchSize int    `toml:"max_inference_batch_size"`
}

// RipCacheConfig defines rip cache settings.
type RipCacheConfig struct {
	Enabled bool `toml:"enabled"`
	MaxGiB  int  `toml:"max_gib"`
}

// DiscIDCacheConfig defines disc ID cache settings.
type DiscIDCacheConfig struct {
	Enabled bool `toml:"enabled"`
}

// MakeMKVConfig defines MakeMKV ripping settings.
type MakeMKVConfig struct {
	OpticalDrive         string `toml:"optical_drive"`
	RipTimeout           int    `toml:"rip_timeout"`
	InfoTimeout          int    `toml:"info_timeout"`
	DiscSettleDelay      int    `toml:"disc_settle_delay"`
	MinTitleLength       int    `toml:"min_title_length"`
	KeyDBPath            string `toml:"keydb_path"`
	KeyDBDownloadURL     string `toml:"keydb_download_url"`
	KeyDBDownloadTimeout int    `toml:"keydb_download_timeout"`
}

// KeyDBTimeout returns the KeyDB download timeout as a time.Duration.
func (m MakeMKVConfig) KeyDBTimeout() time.Duration {
	return time.Duration(m.KeyDBDownloadTimeout) * time.Second
}

// EncodingConfig defines SVT-AV1 encoding settings.
type EncodingConfig struct {
	SVTAV1Preset int `toml:"svt_av1_preset"`
	CRFSD        int `toml:"crf_sd"`
	CRFHD        int `toml:"crf_hd"`
	CRFUHD       int `toml:"crf_uhd"`
}

// LLMConfig defines LLM API settings for OpenRouter.
type LLMConfig struct {
	APIKey         string `toml:"api_key"`
	BaseURL        string `toml:"base_url"`
	Model          string `toml:"model"`
	Referer        string `toml:"referer"`
	Title          string `toml:"title"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

// CommentaryConfig defines commentary track detection settings.
type CommentaryConfig struct {
	Enabled             bool    `toml:"enabled"`
	SimilarityThreshold float64 `toml:"similarity_threshold"`
	ConfidenceThreshold float64 `toml:"confidence_threshold"`
}

// ContentIDConfig defines episode identification policy thresholds.
type ContentIDConfig struct {
	MinSimilarityScore           float64 `toml:"min_similarity_score"`
	ClearMatchMargin             float64 `toml:"clear_match_margin"`
	LowConfidenceReviewThreshold float64 `toml:"low_confidence_review_threshold"`
	LLMVerifyThreshold           float64 `toml:"llm_verify_threshold"`
}

// LoggingConfig defines log retention settings.
type LoggingConfig struct {
	RetentionDays int `toml:"retention_days"`
}

// cacheBaseDir returns the XDG cache base directory for Spindle.
func cacheBaseDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(dir, "spindle")
}

// runtimeDir returns XDG_RUNTIME_DIR with /tmp fallback.
func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return "/tmp"
}

// OpenSubtitlesCacheDir returns the auto-derived OpenSubtitles cache directory.
func (c *Config) OpenSubtitlesCacheDir() string {
	return filepath.Join(cacheBaseDir(), "opensubtitles")
}

// TranscriptionCacheDir returns the auto-derived transcription cache directory.
func (c *Config) TranscriptionCacheDir() string {
	return filepath.Join(cacheBaseDir(), "transcription")
}

// TranscriptionRuntimeDir returns the managed Python runtime root.
func (c *Config) TranscriptionRuntimeDir() string {
	return filepath.Join(cacheBaseDir(), "qwen3-runtime")
}

// RipCacheDir returns the auto-derived rip cache directory.
func (c *Config) RipCacheDir() string {
	return filepath.Join(cacheBaseDir(), "rips")
}

// DiscIDCachePath returns the auto-derived disc ID cache file path.
func (c *Config) DiscIDCachePath() string {
	return filepath.Join(cacheBaseDir(), "discid_cache.json")
}

// QueueDBPath returns the queue database path within the state directory.
func (c *Config) QueueDBPath() string {
	return filepath.Join(c.Paths.StateDir, "queue.db")
}

// SocketPath returns the daemon Unix socket path.
func (c *Config) SocketPath() string {
	return filepath.Join(runtimeDir(), "spindle.sock")
}

// LockPath returns the daemon lock file path.
func (c *Config) LockPath() string {
	return filepath.Join(runtimeDir(), "spindle.lock")
}

// DaemonLogPath returns the daemon log symlink path (points to the active log file).
func (c *Config) DaemonLogPath() string {
	return filepath.Join(c.Paths.StateDir, "daemon.log")
}

// DaemonLogDir returns the directory where timestamped daemon log files are stored.
func (c *Config) DaemonLogDir() string {
	return c.Paths.StateDir
}
