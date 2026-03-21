package config

import (
	"os"
	"path/filepath"
)

// Config holds all Spindle configuration sections.
type Config struct {
	Paths         PathsConfig         `toml:"paths"`
	API           APIConfig           `toml:"api"`
	TMDB          TMDBConfig          `toml:"tmdb"`
	Jellyfin      JellyfinConfig      `toml:"jellyfin"`
	Library       LibraryConfig       `toml:"library"`
	Notifications NotificationsConfig `toml:"notifications"`
	Subtitles     SubtitlesConfig     `toml:"subtitles"`
	RipCache      RipCacheConfig      `toml:"rip_cache"`
	DiscIDCache   DiscIDCacheConfig   `toml:"disc_id_cache"`
	MakeMKV       MakeMKVConfig       `toml:"makemkv"`
	Encoding      EncodingConfig      `toml:"encoding"`
	LLM           LLMConfig           `toml:"llm"`
	Commentary    CommentaryConfig    `toml:"commentary"`
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
	WhisperXModel          string   `toml:"whisperx_model"`
	WhisperXCUDAEnabled    bool     `toml:"whisperx_cuda_enabled"`
	WhisperXVADMethod      string   `toml:"whisperx_vad_method"`
	WhisperXHFToken        string   `toml:"whisperx_hf_token"`
	OpenSubtitlesEnabled   bool     `toml:"opensubtitles_enabled"`
	OpenSubtitlesAPIKey    string   `toml:"opensubtitles_api_key"`
	OpenSubtitlesUserAgent string   `toml:"opensubtitles_user_agent"`
	OpenSubtitlesUserToken string   `toml:"opensubtitles_user_token"`
	OpenSubtitlesLanguages []string `toml:"opensubtitles_languages"`
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

// EncodingConfig defines SVT-AV1 encoding settings.
type EncodingConfig struct {
	SVTAV1Preset int `toml:"svt_av1_preset"`
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
	WhisperXModel       string  `toml:"whisperx_model"`
	SimilarityThreshold float64 `toml:"similarity_threshold"`
	ConfidenceThreshold float64 `toml:"confidence_threshold"`
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

// WhisperXCacheDir returns the auto-derived WhisperX transcription cache directory.
func (c *Config) WhisperXCacheDir() string {
	return filepath.Join(cacheBaseDir(), "whisperx")
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

