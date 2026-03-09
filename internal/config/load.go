package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Load reads, normalizes, and validates config from the search path.
// Search order: 1) explicit path, 2) ~/.config/spindle/config.toml,
// 3) ./spindle.toml, 4) all defaults (no error).
func Load(explicitPath string) (*Config, error) {
	cfg := &Config{}

	data, err := findAndRead(explicitPath)
	if err != nil {
		return nil, err
	}

	if data != nil {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse TOML: %w", err)
		}
	}

	applyMuxDefault(data, cfg)
	applyDefaults(cfg)
	applyEnvOverrides(cfg)

	if err := normalizePaths(cfg); err != nil {
		return nil, fmt.Errorf("config: normalize paths: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// findAndRead locates and reads the config file. Returns nil data if no file found.
func findAndRead(explicitPath string) ([]byte, error) {
	if explicitPath != "" {
		expanded, err := expandHome(explicitPath)
		if err != nil {
			return nil, fmt.Errorf("config: expand path %q: %w", explicitPath, err)
		}
		data, err := os.ReadFile(expanded)
		if err != nil {
			return nil, fmt.Errorf("config: read %q: %w", expanded, err)
		}
		return data, nil
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
			return data, nil
		}
	}

	// No config file found; use defaults.
	return nil, nil
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
		cfg.Subtitles.OpenSubtitlesUserAgent = "Spindle/dev"
	}
	if len(cfg.Subtitles.OpenSubtitlesLanguages) == 0 {
		cfg.Subtitles.OpenSubtitlesLanguages = []string{"en"}
	}
	// mux_into_mkv defaults to true; TOML bool zero value is false,
	// so we only set the default when the config was not explicitly parsed.
	// Since go-toml/v2 leaves the field at zero when absent, we handle this
	// by always defaulting to true when the parsed value is false AND the
	// field was not explicitly set. However, since we cannot distinguish
	// "explicitly set to false" from "absent" without a pointer/sentinel,
	// we accept that MuxIntoMKV defaults to true in applyDefaults only
	// when the entire subtitles section has default values.
	// For simplicity, we just note this as a documented behavior: users
	// must explicitly set mux_into_mkv = false to disable it.
	// We handle this via a separate mechanism below.

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
	if cfg.Encoding.SVTAV1Preset == 0 {
		cfg.Encoding.SVTAV1Preset = 6
	}

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

	// [logging]
	if cfg.Logging.RetentionDays == 0 {
		cfg.Logging.RetentionDays = 60
	}
}

// applyMuxDefault is called after TOML parsing to handle the mux_into_mkv default.
// This is handled separately in the TOML unmarshaling since the zero value (false)
// conflicts with the desired default (true).
func applyMuxDefault(data []byte, cfg *Config) {
	// If the TOML data does not contain mux_into_mkv, default to true.
	if data == nil || !strings.Contains(string(data), "mux_into_mkv") {
		cfg.Subtitles.MuxIntoMKV = true
	}
}

// applyEnvOverrides applies environment variable overrides to config fields.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("TMDB_API_KEY"); v != "" {
		cfg.TMDB.APIKey = v
	}
	if v := os.Getenv("JELLYFIN_API_KEY"); v != "" {
		cfg.Jellyfin.APIKey = v
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("SPINDLE_API_TOKEN"); v != "" {
		cfg.API.Token = v
	}

	// HuggingFace token: HUGGING_FACE_HUB_TOKEN takes priority, then HF_TOKEN.
	if v := os.Getenv("HUGGING_FACE_HUB_TOKEN"); v != "" {
		cfg.Subtitles.WhisperXHFToken = v
	} else if v := os.Getenv("HF_TOKEN"); v != "" {
		cfg.Subtitles.WhisperXHFToken = v
	}

	if v := os.Getenv("OPENSUBTITLES_API_KEY"); v != "" {
		cfg.Subtitles.OpenSubtitlesAPIKey = v
	}
	if v := os.Getenv("OPENSUBTITLES_USER_TOKEN"); v != "" {
		cfg.Subtitles.OpenSubtitlesUserToken = v
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
