package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config encapsulates all configuration values for the Go implementation of Spindle.
type Config struct {
	StagingDir                    string   `toml:"staging_dir"`
	LibraryDir                    string   `toml:"library_dir"`
	LogDir                        string   `toml:"log_dir"`
	OpenSubtitlesCacheDir         string   `toml:"opensubtitles_cache_dir"`
	WhisperXCacheDir              string   `toml:"whisperx_cache_dir"`
	DraptoLogDir                  string   `toml:"drapto_log_dir"`
	ReviewDir                     string   `toml:"review_dir"`
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
	DraptoPreset                  int      `toml:"drapto_preset"`
	SubtitlesEnabled              bool     `toml:"subtitles_enabled"`
	WhisperXCUDAEnabled           bool     `toml:"whisperx_cuda_enabled"`
	WhisperXVADMethod             string   `toml:"whisperx_vad_method"`
	WhisperXHuggingFaceToken      string   `toml:"whisperx_hf_token"`
	OpenSubtitlesEnabled          bool     `toml:"opensubtitles_enabled"`
	OpenSubtitlesAPIKey           string   `toml:"opensubtitles_api_key"`
	OpenSubtitlesUserAgent        string   `toml:"opensubtitles_user_agent"`
	OpenSubtitlesUserToken        string   `toml:"opensubtitles_user_token"`
	OpenSubtitlesLanguages        []string `toml:"opensubtitles_languages"`
}

const (
	defaultStagingDir                  = "~/.local/share/spindle/staging"
	defaultLibraryDir                  = "~/library"
	defaultLogDir                      = "~/.local/share/spindle/logs"
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
	defaultDraptoPreset                = 4
	defaultPlexAuthPath                = "~/.config/spindle/plex_auth.json"
	defaultDraptoLogDir                = "~/.local/share/spindle/logs/drapto"
	defaultKeyDBPath                   = "~/.config/spindle/keydb/KEYDB.cfg"
	defaultKeyDBDownloadURL            = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"
	defaultKeyDBDownloadTimeout        = 300
	defaultIdentificationOverridesPath = "~/.config/spindle/overrides/identification.json"
	defaultOpenSubtitlesUserAgent      = "Spindle/dev"
)

// Default returns a Config populated with repository defaults.
func Default() Config {
	return Config{
		StagingDir:                  defaultStagingDir,
		LibraryDir:                  defaultLibraryDir,
		LogDir:                      defaultLogDir,
		OpenSubtitlesCacheDir:       defaultOpenSubtitlesCacheDir,
		WhisperXCacheDir:            defaultWhisperXCacheDir,
		DraptoLogDir:                defaultDraptoLogDir,
		ReviewDir:                   defaultReviewDir,
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
		DraptoPreset:                defaultDraptoPreset,
		WhisperXVADMethod:           "silero",
		OpenSubtitlesLanguages:      []string{"en"},
		OpenSubtitlesUserAgent:      defaultOpenSubtitlesUserAgent,
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
	if c.ReviewDir, err = expandPath(c.ReviewDir); err != nil {
		return fmt.Errorf("review_dir: %w", err)
	}
	if c.DraptoLogDir, err = expandPath(c.DraptoLogDir); err != nil {
		return fmt.Errorf("drapto_log_dir: %w", err)
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
	if strings.TrimSpace(c.DraptoLogDir) == "" {
		if strings.TrimSpace(c.LogDir) == "" {
			c.DraptoLogDir = ""
		} else {
			c.DraptoLogDir = filepath.Join(c.LogDir, "drapto")
		}
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
	if c.DraptoPreset < 0 {
		return errors.New("drapto_preset must be zero or positive")
	}
	if strings.TrimSpace(c.KeyDBDownloadURL) == "" {
		return errors.New("keydb_download_url must be set")
	}
	if c.KeyDBDownloadTimeout <= 0 {
		return errors.New("keydb_download_timeout must be positive (seconds)")
	}
	if strings.TrimSpace(c.DraptoLogDir) == "" {
		return errors.New("drapto_log_dir must be set")
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
	return nil
}

// EnsureDirectories creates required directories for daemon operation.
func (c *Config) EnsureDirectories() error {
	for _, dir := range []string{c.StagingDir, c.LibraryDir, c.LogDir, c.ReviewDir, c.DraptoLogDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
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

// DraptoCurrentLogPath returns the pointer file used for the most recent Drapto log.
func (c *Config) DraptoCurrentLogPath() string {
	if c == nil {
		return ""
	}
	base := strings.TrimSpace(c.LogDir)
	if base == "" {
		return ""
	}
	return filepath.Join(base, "drapto.log")
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

// CreateSample writes a sample configuration file to the specified location.
func CreateSample(path string) error {
	sample := `# Spindle Configuration
# ====================
# Edit the REQUIRED settings below, then customize optional settings when needed.

# ============================================================================
# REQUIRED SETTINGS - Update these before running Spindle
# ============================================================================

# TMDB API (required for media identification)
tmdb_api_key = "your_tmdb_api_key_here"              # Get from themoviedb.org/settings/api

# Directory paths - adjust for your environment
library_dir = "~/your-media-library"                 # MUST EXIST: Final media library directory
movies_dir = "movies"                                # Subdirectory inside library_dir for movies
tv_dir = "tv"                                        # Subdirectory inside library_dir for TV

# Library import behavior
overwrite_existing_library_files = false             # Set true to replace existing MKV/SRT files in the library

# ============================================================================
# PATHS & HARDWARE
# ============================================================================

staging_dir = "~/.local/share/spindle/staging"       # Working directory for rips/encodes
log_dir = "~/.local/share/spindle/logs"              # Logs and queue database
drapto_log_dir = "~/.local/share/spindle/logs/drapto" # Drapto encoder log files
review_dir = "~/review"                              # Encoded files awaiting manual identification
optical_drive = "/dev/sr0"                           # Optical drive device path
api_bind = "127.0.0.1:7487"                          # HTTP API bind address (host:port)

# ============================================================================
# OPTIONAL SERVICES
# ============================================================================

# Plex link (Plex library scanning)
plex_link_enabled = true                             # If false, Spindle will not trigger Plex scans automatically
plex_url = "http://localhost:32400"                  # Plex server URL (omit to disable)
plex_auth_path = "~/.config/spindle/plex_auth.json"  # Location for stored Plex authorization tokens
movies_library = "Movies"                            # Plex movie library name
tv_library = "TV Shows"                              # Plex TV library name

# Notifications
ntfy_topic = "https://ntfy.sh/your_topic"            # ntfy topic for push notifications (optional)
ntfy_request_timeout = 10                            # ntfy HTTP client timeout (seconds)

# AI-generated subtitles (optional)
subtitles_enabled = false                            # Enable WhisperX subtitle generation after encoding
whisperx_cuda_enabled = false                        # Run WhisperX with CUDA; set true when GPU + CUDA/cuDNN are installed
whisperx_vad_method = "silero"                       # Voice activity detector: "silero" (default, no token) or "pyannote" (requires Hugging Face token)
whisperx_hf_token = ""                               # Hugging Face access token for pyannote VAD (leave empty when using silero)
whisperx_cache_dir = "~/.local/share/spindle/cache/whisperx" # Cache for WhisperX transcripts shared between stages
opensubtitles_enabled = false                        # Set true to fetch subtitles from OpenSubtitles before falling back to WhisperX
opensubtitles_api_key = "your_opensubtitles_api_key_here" # Required when opensubtitles_enabled is true; create at opensubtitles.com/consumers
opensubtitles_user_agent = "Spindle/<version>"       # Custom User-Agent header registered with OpenSubtitles
opensubtitles_languages = ["en"]                     # Preferred subtitle languages (ISO 639-1 codes, e.g., ["en","es"])
opensubtitles_user_token = ""                        # Optional OpenSubtitles user JWT for higher download limits

# ============================================================================
# TMDB & METADATA
# ============================================================================

tmdb_language = "en-US"                              # ISO 639-1 language for TMDB metadata
tmdb_base_url = "https://api.themoviedb.org/3"       # Override when using a TMDB proxy
tmdb_confidence_threshold = 0.8                      # Match confidence (0.0-1.0)
keydb_path = "~/.config/spindle/keydb/KEYDB.cfg"     # Optional KEYDB.cfg for Disc ID lookups (leave empty to disable)
keydb_download_url = "http://fvonline-db.bplaced.net/export/keydb_eng.zip" # Mirror for automatic KEYDB refreshes
keydb_download_timeout = 1500                        # Download timeout in seconds when refreshing KEYDB
identification_overrides_path = "~/.config/spindle/overrides/identification.json" # Optional JSON containing curated disc overrides

# ============================================================================
# ENCODING
# ============================================================================

drapto_preset = 4                                    # Drapto SVT-AV1 preset (lower is faster, higher is higher quality)

# ============================================================================
# WORKFLOW TUNING (ADVANCED)
# ============================================================================

makemkv_rip_timeout = 3600                           # MakeMKV ripping timeout (seconds)
queue_poll_interval = 5                              # Queue polling cadence (seconds)
error_retry_interval = 10                            # Delay before retrying failures (seconds)
workflow_heartbeat_interval = 15                     # Worker heartbeat interval (seconds)
workflow_heartbeat_timeout = 120                     # Worker heartbeat timeout (seconds)

# ============================================================================
# LOGGING
# ============================================================================

log_format = "console"                              # "console" or "json"
log_level = "info"                                  # info, debug, warn, error
`

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
