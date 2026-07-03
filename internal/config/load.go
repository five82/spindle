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
	cfg := defaultConfig()

	data, source, resolvedPath, err := findAndRead(explicitPath)
	if err != nil {
		return nil, err
	}

	if data != nil {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse TOML: %w", err)
		}
	}
	cfg.SourcePath = resolvedPath

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

// defaultConfig returns the complete built-in configuration before file and
// environment overrides are applied.
func defaultConfig() *Config {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/"
	}

	return &Config{
		Paths: PathsConfig{
			StagingDir: filepath.Join(home, ".local", "share", "spindle", "staging"),
			LibraryDir: filepath.Join(home, "library"),
			StateDir:   filepath.Join(home, ".local", "state", "spindle"),
			ReviewDir:  filepath.Join(home, "review"),
		},
		TMDB: TMDBConfig{
			BaseURL:  "https://api.themoviedb.org/3",
			Language: "en-US",
		},
		Library: LibraryConfig{
			MoviesDir: "movies",
			TVDir:     "tv",
		},
		Notifications: NotificationsConfig{
			RequestTimeout: 10,
		},
		Subtitles: SubtitlesConfig{
			MuxIntoMKV:             true,
			WhisperXModel:          "large-v3",
			WhisperXVADMethod:      "silero",
			OpenSubtitlesUserAgent: "Spindle/dev v0.1.0",
			OpenSubtitlesLanguages: []string{"en"},
		},
		RipCache: RipCacheConfig{
			MaxGiB: 150,
		},
		MakeMKV: MakeMKVConfig{
			OpticalDrive:         "/dev/sr0",
			RipTimeout:           14400,
			InfoTimeout:          600,
			DiscSettleDelay:      10,
			MinTitleLength:       120,
			KeyDBPath:            filepath.Join(home, ".config", "spindle", "keydb", "KEYDB.cfg"),
			KeyDBDownloadURL:     "http://fvonline-db.bplaced.net/export/keydb_eng.zip",
			KeyDBDownloadTimeout: 300,
		},
		LLM: LLMConfig{
			BaseURL:        "https://openrouter.ai/api/v1/chat/completions",
			Model:          "google/gemini-3-flash-preview",
			Referer:        "https://github.com/five82/spindle",
			Title:          "Spindle",
			TimeoutSeconds: 60,
		},
		Commentary: CommentaryConfig{
			SimilarityThreshold: 0.92,
			ConfidenceThreshold: 0.80,
		},
		ContentID: ContentIDConfig{
			MinSimilarityScore:           0.58,
			ClearMatchMargin:             0.05,
			LowConfidenceReviewThreshold: 0.70,
			DecisiveAutoAcceptThreshold:  0.80,
			ClearConfidenceThreshold:     0.85,
		},
		Logging: LoggingConfig{
			RetentionDays: 60,
		},
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
