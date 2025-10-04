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
	StagingDir                string  `toml:"staging_dir"`
	LibraryDir                string  `toml:"library_dir"`
	LogDir                    string  `toml:"log_dir"`
	ReviewDir                 string  `toml:"review_dir"`
	OpticalDrive              string  `toml:"optical_drive"`
	TMDBAPIKey                string  `toml:"tmdb_api_key"`
	TMDBBaseURL               string  `toml:"tmdb_base_url"`
	TMDBLanguage              string  `toml:"tmdb_language"`
	TMDBConfidenceThreshold   float64 `toml:"tmdb_confidence_threshold"`
	MoviesDir                 string  `toml:"movies_dir"`
	TVDir                     string  `toml:"tv_dir"`
	PlexLinkEnabled           bool    `toml:"plex_link_enabled"`
	PlexURL                   string  `toml:"plex_url"`
	MoviesLibrary             string  `toml:"movies_library"`
	TVLibrary                 string  `toml:"tv_library"`
	NtfyTopic                 string  `toml:"ntfy_topic"`
	MakeMKVRipTimeout         int     `toml:"makemkv_rip_timeout"`
	MakeMKVInfoTimeout        int     `toml:"makemkv_info_timeout"`
	NtfyRequestTimeout        int     `toml:"ntfy_request_timeout"`
	DiscMonitorTimeout        int     `toml:"disc_monitor_timeout"`
	QueuePollInterval         int     `toml:"queue_poll_interval"`
	ErrorRetryInterval        int     `toml:"error_retry_interval"`
	WorkflowHeartbeatInterval int     `toml:"workflow_heartbeat_interval"`
	WorkflowHeartbeatTimeout  int     `toml:"workflow_heartbeat_timeout"`
	LogFormat                 string  `toml:"log_format"`
	LogLevel                  string  `toml:"log_level"`
}

const (
	defaultStagingDir                = "~/.local/share/spindle/staging"
	defaultLibraryDir                = "~/library"
	defaultLogDir                    = "~/.local/share/spindle/logs"
	defaultReviewDir                 = "~/review"
	defaultOpticalDrive              = "/dev/sr0"
	defaultMoviesDir                 = "movies"
	defaultTVDir                     = "tv"
	defaultTMDBLanguage              = "en-US"
	defaultTMDBBaseURL               = "https://api.themoviedb.org/3"
	defaultLogFormat                 = "console"
	defaultLogLevel                  = "info"
	defaultWorkflowHeartbeatInterval = 15
	defaultWorkflowHeartbeatTimeout  = 120
)

// Default returns a Config populated with repository defaults.
func Default() Config {
	return Config{
		StagingDir:                defaultStagingDir,
		LibraryDir:                defaultLibraryDir,
		LogDir:                    defaultLogDir,
		ReviewDir:                 defaultReviewDir,
		OpticalDrive:              defaultOpticalDrive,
		TMDBLanguage:              defaultTMDBLanguage,
		TMDBBaseURL:               defaultTMDBBaseURL,
		TMDBConfidenceThreshold:   0.8,
		MoviesDir:                 defaultMoviesDir,
		TVDir:                     defaultTVDir,
		PlexLinkEnabled:           true,
		MoviesLibrary:             "Movies",
		TVLibrary:                 "TV Shows",
		MakeMKVRipTimeout:         3600,
		MakeMKVInfoTimeout:        300,
		NtfyRequestTimeout:        10,
		DiscMonitorTimeout:        5,
		QueuePollInterval:         5,
		ErrorRetryInterval:        10,
		WorkflowHeartbeatInterval: defaultWorkflowHeartbeatInterval,
		WorkflowHeartbeatTimeout:  defaultWorkflowHeartbeatTimeout,
		LogFormat:                 defaultLogFormat,
		LogLevel:                  defaultLogLevel,
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
	if c.ReviewDir, err = expandPath(c.ReviewDir); err != nil {
		return fmt.Errorf("review_dir: %w", err)
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
	return nil
}

// EnsureDirectories creates required directories for daemon operation.
func (c *Config) EnsureDirectories() error {
	for _, dir := range []string{c.StagingDir, c.LibraryDir, c.LogDir, c.ReviewDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
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

# ============================================================================
# PATHS & HARDWARE
# ============================================================================

staging_dir = "~/.local/share/spindle/staging"       # Working directory for rips/encodes
log_dir = "~/.local/share/spindle/logs"              # Logs and queue database
review_dir = "~/review"                              # Encoded files awaiting manual identification
optical_drive = "/dev/sr0"                           # Optical drive device path

# ============================================================================
# OPTIONAL SERVICES
# ============================================================================

# Plex link (Plex library scanning)
plex_link_enabled = true                             # If false, Spindle will not trigger Plex scans automatically
plex_url = "http://localhost:32400"                  # Plex server URL (omit to disable)
movies_library = "Movies"                            # Plex movie library name
tv_library = "TV Shows"                              # Plex TV library name

# Notifications
ntfy_topic = "https://ntfy.sh/your_topic"            # ntfy topic for push notifications (optional)
ntfy_request_timeout = 10                            # ntfy HTTP client timeout (seconds)

# ============================================================================
# TMDB & METADATA
# ============================================================================

tmdb_language = "en-US"                              # ISO 639-1 language for TMDB metadata
tmdb_base_url = "https://api.themoviedb.org/3"       # Override when using a TMDB proxy
tmdb_confidence_threshold = 0.8                      # Match confidence (0.0-1.0)

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
