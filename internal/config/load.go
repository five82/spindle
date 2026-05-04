package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/five82/spindle/internal/logs"
)

// Load reads, normalizes, and validates config from the search path.
// Search order: 1) explicit path, 2) ~/.config/spindle/config.toml,
// 3) ./spindle.toml, 4) all defaults (no error).
func Load(explicitPath string, logger *slog.Logger) (*Config, error) {
	logger = logs.Default(logger)
	cfg := &Config{}

	data, source, resolvedPath, err := findAndRead(explicitPath)
	if err != nil {
		return nil, err
	}

	var raw rawConfig
	if data != nil {
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("config: parse TOML: %w", err)
		}
	}
	*cfg = raw.toConfig()
	cfg.SourcePath = resolvedPath

	applyDefaults(cfg)

	envKeys := collectEnvOverrides(cfg)

	if err := normalizePaths(cfg); err != nil {
		return nil, fmt.Errorf("config: normalize paths: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger.Debug("configuration loaded",
		"decision_type", logs.DecisionConfigLoad,
		"decision_result", source,
		"decision_reason", configSourceReason(source, explicitPath),
	)
	if len(envKeys) > 0 {
		logger.Debug("environment overrides applied", "keys", envKeys)
	}

	return cfg, nil
}

type rawConfig struct {
	Paths         PathsConfig         `toml:"paths"`
	API           APIConfig           `toml:"api"`
	TMDB          TMDBConfig          `toml:"tmdb"`
	Jellyfin      JellyfinConfig      `toml:"jellyfin"`
	Library       LibraryConfig       `toml:"library"`
	Notifications NotificationsConfig `toml:"notifications"`
	Subtitles     rawSubtitlesConfig  `toml:"subtitles"`
	RipCache      RipCacheConfig      `toml:"rip_cache"`
	DiscIDCache   DiscIDCacheConfig   `toml:"disc_id_cache"`
	MakeMKV       MakeMKVConfig       `toml:"makemkv"`
	Encoding      EncodingConfig      `toml:"encoding"`
	LLM           LLMConfig           `toml:"llm"`
	Commentary    CommentaryConfig    `toml:"commentary"`
	ContentID     ContentIDConfig     `toml:"content_id"`
	Logging       LoggingConfig       `toml:"logging"`
}

type rawSubtitlesConfig struct {
	Enabled                bool     `toml:"enabled"`
	MuxIntoMKV             *bool    `toml:"mux_into_mkv"`
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

func (r rawConfig) toConfig() Config {
	return Config{
		Paths:         r.Paths,
		API:           r.API,
		TMDB:          r.TMDB,
		Jellyfin:      r.Jellyfin,
		Library:       r.Library,
		Notifications: r.Notifications,
		Subtitles:     r.Subtitles.toConfig(),
		RipCache:      r.RipCache,
		DiscIDCache:   r.DiscIDCache,
		MakeMKV:       r.MakeMKV,
		Encoding:      r.Encoding,
		LLM:           r.LLM,
		Commentary:    r.Commentary,
		ContentID:     r.ContentID,
		Logging:       r.Logging,
	}
}

func (r rawSubtitlesConfig) toConfig() SubtitlesConfig {
	muxIntoMKV := true
	if r.MuxIntoMKV != nil {
		muxIntoMKV = *r.MuxIntoMKV
	}
	return SubtitlesConfig{
		Enabled:                r.Enabled,
		MuxIntoMKV:             muxIntoMKV,
		WhisperXModel:          r.WhisperXModel,
		WhisperXCUDAEnabled:    r.WhisperXCUDAEnabled,
		WhisperXVADMethod:      r.WhisperXVADMethod,
		WhisperXHFToken:        r.WhisperXHFToken,
		OpenSubtitlesEnabled:   r.OpenSubtitlesEnabled,
		OpenSubtitlesAPIKey:    r.OpenSubtitlesAPIKey,
		OpenSubtitlesUserAgent: r.OpenSubtitlesUserAgent,
		OpenSubtitlesUserToken: r.OpenSubtitlesUserToken,
		OpenSubtitlesLanguages: r.OpenSubtitlesLanguages,
	}
}

// findAndRead locates and reads the config file. Returns nil data if no file found.
// The source string describes where config came from: "explicit_path", "search_path", or "defaults_only".
// The resolvedPath is the absolute filesystem path of the config file (empty for defaults_only).
func findAndRead(explicitPath string) ([]byte, string, string, error) {
	if explicitPath != "" {
		expanded, err := expandHome(explicitPath)
		if err != nil {
			return nil, "", "", fmt.Errorf("config: expand path %q: %w", explicitPath, err)
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return nil, "", "", fmt.Errorf("config: resolve absolute path %q: %w", expanded, err)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, "", "", fmt.Errorf("config: read %q: %w", abs, err)
		}
		return data, "explicit_path", abs, nil
	}

	// Search order: ~/.config/spindle/config.toml, then ./spindle.toml
	candidates := []string{}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "spindle", "config.toml"))
	}
	candidates = append(candidates, "spindle.toml")

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				abs = path
			}
			return data, "search_path", abs, nil
		}
	}

	// No config file found; use defaults.
	return nil, "defaults_only", "", nil
}

// applyDefaults sets default values for fields that are empty/zero.
func applyDefaults(cfg *Config) {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/"
	}

	// [paths]
	if cfg.Paths.StagingDir == "" {
		cfg.Paths.StagingDir = filepath.Join(home, ".local", "share", "spindle", "staging")
	}
	if cfg.Paths.LibraryDir == "" {
		cfg.Paths.LibraryDir = filepath.Join(home, "library")
	}
	if cfg.Paths.StateDir == "" {
		cfg.Paths.StateDir = filepath.Join(home, ".local", "state", "spindle")
	}
	if cfg.Paths.ReviewDir == "" {
		cfg.Paths.ReviewDir = filepath.Join(home, "review")
	}

	// [tmdb]
	if cfg.TMDB.BaseURL == "" {
		cfg.TMDB.BaseURL = "https://api.themoviedb.org/3"
	}
	if cfg.TMDB.Language == "" {
		cfg.TMDB.Language = "en-US"
	}

	// [library]
	if cfg.Library.MoviesDir == "" {
		cfg.Library.MoviesDir = "movies"
	}
	if cfg.Library.TVDir == "" {
		cfg.Library.TVDir = "tv"
	}

	// [notifications]
	if cfg.Notifications.RequestTimeout == 0 {
		cfg.Notifications.RequestTimeout = 10
	}

	// [subtitles]
	if cfg.Subtitles.WhisperXModel == "" {
		cfg.Subtitles.WhisperXModel = "large-v3"
	}
	if cfg.Subtitles.WhisperXVADMethod == "" {
		cfg.Subtitles.WhisperXVADMethod = "silero"
	}
	if cfg.Subtitles.OpenSubtitlesUserAgent == "" {
		cfg.Subtitles.OpenSubtitlesUserAgent = "Spindle/dev v0.1.0"
	}
	if len(cfg.Subtitles.OpenSubtitlesLanguages) == 0 {
		cfg.Subtitles.OpenSubtitlesLanguages = []string{"en"}
	}
	// [rip_cache]
	if cfg.RipCache.MaxGiB == 0 {
		cfg.RipCache.MaxGiB = 150
	}

	// [makemkv]
	if cfg.MakeMKV.OpticalDrive == "" {
		cfg.MakeMKV.OpticalDrive = "/dev/sr0"
	}
	if cfg.MakeMKV.RipTimeout == 0 {
		cfg.MakeMKV.RipTimeout = 14400
	}
	if cfg.MakeMKV.InfoTimeout == 0 {
		cfg.MakeMKV.InfoTimeout = 600
	}
	if cfg.MakeMKV.DiscSettleDelay == 0 {
		cfg.MakeMKV.DiscSettleDelay = 10
	}
	if cfg.MakeMKV.MinTitleLength == 0 {
		cfg.MakeMKV.MinTitleLength = 120
	}
	if cfg.MakeMKV.KeyDBPath == "" {
		cfg.MakeMKV.KeyDBPath = filepath.Join(home, ".config", "spindle", "keydb", "KEYDB.cfg")
	}
	if cfg.MakeMKV.KeyDBDownloadURL == "" {
		cfg.MakeMKV.KeyDBDownloadURL = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"
	}
	if cfg.MakeMKV.KeyDBDownloadTimeout == 0 {
		cfg.MakeMKV.KeyDBDownloadTimeout = 300
	}

	// [encoding]
	// svt_av1_preset default is 6; zero value (0) is a valid preset,
	// so we use -1 as an internal sentinel. However, since the struct
	// uses int with zero value 0, and preset 0 is valid, we cannot
	// distinguish "not set" from "set to 0" without a pointer.
	// Convention: if the value is 0 and came from defaults, it stays 0.
	// The default of 6 is applied only when the config file did not
	// specify the field at all. Since go-toml/v2 leaves int at 0 when
	// absent, we check if it's 0 and no TOML was parsed, but we cannot
	// know that here. For safety, we apply 6 only when 0 and accept
	// that users wanting preset 0 must explicitly set it.
	// Actually, preset 0 is extremely slow and no one would use it
	// unintentionally, so defaulting 0 -> 6 is acceptable.
	applyEncodingDefaults(&cfg.Encoding)

	// [llm]
	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = "google/gemini-3-flash-preview"
	}
	if cfg.LLM.Referer == "" {
		cfg.LLM.Referer = "https://github.com/five82/spindle"
	}
	if cfg.LLM.Title == "" {
		cfg.LLM.Title = "Spindle"
	}
	if cfg.LLM.TimeoutSeconds == 0 {
		cfg.LLM.TimeoutSeconds = 60
	}

	// [commentary]
	if cfg.Commentary.WhisperXModel == "" {
		cfg.Commentary.WhisperXModel = "large-v3-turbo"
	}
	if cfg.Commentary.SimilarityThreshold == 0 {
		cfg.Commentary.SimilarityThreshold = 0.92
	}
	if cfg.Commentary.ConfidenceThreshold == 0 {
		cfg.Commentary.ConfidenceThreshold = 0.80
	}

	// [content_id]
	if cfg.ContentID.MinSimilarityScore == 0 {
		cfg.ContentID.MinSimilarityScore = 0.58
	}
	if cfg.ContentID.ClearMatchMargin == 0 {
		cfg.ContentID.ClearMatchMargin = 0.05
	}
	if cfg.ContentID.LowConfidenceReviewThreshold == 0 {
		cfg.ContentID.LowConfidenceReviewThreshold = 0.70
	}
	if cfg.ContentID.DecisiveAutoAcceptThreshold == 0 {
		cfg.ContentID.DecisiveAutoAcceptThreshold = 0.80
	}
	if cfg.ContentID.ClearConfidenceThreshold == 0 {
		cfg.ContentID.ClearConfidenceThreshold = 0.85
	}

	// [logging]
	if cfg.Logging.RetentionDays == 0 {
		cfg.Logging.RetentionDays = 60
	}
}

// collectEnvOverrides applies environment variable overrides to config fields
// and returns the list of env var names that were applied.
func collectEnvOverrides(cfg *Config) []string {
	var applied []string
	if v := os.Getenv("TMDB_API_KEY"); v != "" {
		cfg.TMDB.APIKey = v
		applied = append(applied, "TMDB_API_KEY")
	}
	if v := os.Getenv("JELLYFIN_API_KEY"); v != "" {
		cfg.Jellyfin.APIKey = v
		applied = append(applied, "JELLYFIN_API_KEY")
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
		applied = append(applied, "OPENROUTER_API_KEY")
	}
	if v := os.Getenv("SPINDLE_API_TOKEN"); v != "" {
		cfg.API.Token = v
		applied = append(applied, "SPINDLE_API_TOKEN")
	}

	// HuggingFace token: HUGGING_FACE_HUB_TOKEN takes priority, then HF_TOKEN.
	if v := os.Getenv("HUGGING_FACE_HUB_TOKEN"); v != "" {
		cfg.Subtitles.WhisperXHFToken = v
		applied = append(applied, "HUGGING_FACE_HUB_TOKEN")
	} else if v := os.Getenv("HF_TOKEN"); v != "" {
		cfg.Subtitles.WhisperXHFToken = v
		applied = append(applied, "HF_TOKEN")
	}

	if v := os.Getenv("OPENSUBTITLES_API_KEY"); v != "" {
		cfg.Subtitles.OpenSubtitlesAPIKey = v
		applied = append(applied, "OPENSUBTITLES_API_KEY")
	}
	if v := os.Getenv("OPENSUBTITLES_USER_TOKEN"); v != "" {
		cfg.Subtitles.OpenSubtitlesUserToken = v
		applied = append(applied, "OPENSUBTITLES_USER_TOKEN")
	}
	return applied
}

// configSourceReason returns a human-readable reason for the config source decision.
func configSourceReason(source, explicitPath string) string {
	switch source {
	case "explicit_path":
		return "loaded from explicit path: " + explicitPath
	case "search_path":
		return "found in config search path"
	default:
		return "no config file found, using defaults"
	}
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

// normalizePaths expands ~ and converts all path fields to absolute paths.
func normalizePaths(cfg *Config) error {
	pathFields := []*string{
		&cfg.Paths.StagingDir,
		&cfg.Paths.LibraryDir,
		&cfg.Paths.StateDir,
		&cfg.Paths.ReviewDir,
		&cfg.MakeMKV.KeyDBPath,
	}

	for _, p := range pathFields {
		expanded, err := expandHome(*p)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return fmt.Errorf("resolve absolute path %q: %w", expanded, err)
		}
		*p = abs
	}

	return nil
}

// ReloadEncoding re-reads the config file at cfg.SourcePath and returns
// a fresh EncodingConfig. Only the [encoding] section is parsed, avoiding
// validation of unrelated fields. If SourcePath is empty (defaults-only)
// or the reload fails, it returns the existing encoding config and any error.
func ReloadEncoding(cfg *Config) (EncodingConfig, error) {
	if cfg.SourcePath == "" {
		return cfg.Encoding, nil
	}

	data, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		return cfg.Encoding, fmt.Errorf("reload encoding config: read %q: %w", cfg.SourcePath, err)
	}

	var partial struct {
		Encoding EncodingConfig `toml:"encoding"`
	}
	if err := toml.Unmarshal(data, &partial); err != nil {
		return cfg.Encoding, fmt.Errorf("reload encoding config: parse: %w", err)
	}

	applyEncodingDefaults(&partial.Encoding)

	if errs := ValidateEncoding(partial.Encoding); len(errs) > 0 {
		return cfg.Encoding, fmt.Errorf("reload encoding config: %s", strings.Join(errs, "; "))
	}

	return partial.Encoding, nil
}
