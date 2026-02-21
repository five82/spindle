package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"spindle/internal/config"
)

func TestLoadDefaultConfigUsesEnvTMDBKeyAndExpandsPaths(t *testing.T) {
	t.Setenv("TMDB_API_KEY", "test-key")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg, resolved, exists, err := config.Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if resolved == "" {
		t.Fatal("expected resolved path")
	}
	if exists {
		t.Fatal("expected config file to be absent in temp HOME")
	}

	wantStaging := filepath.Join(tempHome, ".local", "share", "spindle", "staging")
	if cfg.Paths.StagingDir != wantStaging {
		t.Fatalf("unexpected staging dir: got %q want %q", cfg.Paths.StagingDir, wantStaging)
	}
	if cfg.Paths.LibraryDir != filepath.Join(tempHome, "library") {
		t.Fatalf("unexpected library dir: %q", cfg.Paths.LibraryDir)
	}
	if cfg.Jellyfin.Enabled {
		t.Fatal("expected Jellyfin disabled by default")
	}
	if cfg.Paths.APIBind != "127.0.0.1:7487" {
		t.Fatalf("unexpected api bind: %q", cfg.Paths.APIBind)
	}
	if cfg.TMDB.APIKey != "test-key" {
		t.Fatalf("expected TMDB key from env, got %q", cfg.TMDB.APIKey)
	}
	if cfg.TMDB.BaseURL != config.Default().TMDB.BaseURL {
		t.Fatalf("unexpected TMDB base url: %q", cfg.TMDB.BaseURL)
	}
	if cfg.Subtitles.Enabled {
		t.Fatal("expected subtitles disabled by default")
	}
	if cfg.Subtitles.WhisperXCUDAEnabled {
		t.Fatal("expected WhisperX CUDA disabled by default")
	}
	if cfg.Subtitles.WhisperXVADMethod != "silero" {
		t.Fatalf("expected WhisperX VAD default to silero, got %q", cfg.Subtitles.WhisperXVADMethod)
	}
	if cfg.Subtitles.WhisperXHuggingFace != "" {
		t.Fatalf("expected WhisperX Hugging Face token to be empty by default, got %q", cfg.Subtitles.WhisperXHuggingFace)
	}
	if cfg.Subtitles.OpenSubtitlesEnabled {
		t.Fatal("expected OpenSubtitles integration disabled by default")
	}
	if cfg.Subtitles.OpenSubtitlesAPIKey != "" {
		t.Fatalf("expected OpenSubtitles API key to be empty by default, got %q", cfg.Subtitles.OpenSubtitlesAPIKey)
	}
	if cfg.Subtitles.OpenSubtitlesUserToken != "" {
		t.Fatalf("expected OpenSubtitles user token to be empty by default, got %q", cfg.Subtitles.OpenSubtitlesUserToken)
	}
	if cfg.Subtitles.OpenSubtitlesUserAgent == "" {
		t.Fatalf("expected OpenSubtitles user agent to have default value")
	}
	if len(cfg.Subtitles.OpenSubtitlesLanguages) == 0 {
		t.Fatalf("expected OpenSubtitles languages to include defaults")
	}
	if cfg.Subtitles.OpenSubtitlesLanguages[0] != "en" {
		t.Fatalf("expected OpenSubtitles default language to be en, got %v", cfg.Subtitles.OpenSubtitlesLanguages)
	}
	if cfg.Workflow.HeartbeatInterval != config.Default().Workflow.HeartbeatInterval {
		t.Fatalf("unexpected heartbeat interval: %d", cfg.Workflow.HeartbeatInterval)
	}
	if cfg.Workflow.HeartbeatTimeout != config.Default().Workflow.HeartbeatTimeout {
		t.Fatalf("unexpected heartbeat timeout: %d", cfg.Workflow.HeartbeatTimeout)
	}
	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	for _, dir := range []string{cfg.Paths.StagingDir, cfg.Paths.LibraryDir, cfg.Paths.LogDir, cfg.Paths.ReviewDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("expected directory %q to exist: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %q to be directory", dir)
		}
	}
}

func TestLoadCustomPath(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "spindle.toml")

	type payload struct {
		TMDB struct {
			APIKey  string `toml:"api_key"`
			BaseURL string `toml:"base_url"`
		} `toml:"tmdb"`
		Library struct {
			MoviesDir string `toml:"movies_dir"`
		} `toml:"library"`
		Workflow struct {
			HeartbeatInterval int `toml:"heartbeat_interval"`
			HeartbeatTimeout  int `toml:"heartbeat_timeout"`
		} `toml:"workflow"`
	}
	custom := payload{}
	custom.TMDB.APIKey = "abc123"
	custom.TMDB.BaseURL = "https://example.com/tmdb"
	custom.Library.MoviesDir = "custom"
	custom.Workflow.HeartbeatInterval = 20
	custom.Workflow.HeartbeatTimeout = 200
	data, err := toml.Marshal(custom)
	if err != nil {
		t.Fatalf("marshal custom config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	cfg, resolved, exists, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !exists {
		t.Fatal("expected exists to be true")
	}
	if resolved != configPath {
		t.Fatalf("unexpected resolved path: got %q want %q", resolved, configPath)
	}
	if cfg.TMDB.APIKey != "abc123" {
		t.Fatalf("expected TMDB key from file, got %q", cfg.TMDB.APIKey)
	}
	if cfg.Library.MoviesDir != "custom" {
		t.Fatalf("expected MoviesDir to be 'custom', got %q", cfg.Library.MoviesDir)
	}
	if cfg.TMDB.BaseURL != "https://example.com/tmdb" {
		t.Fatalf("expected TMDB base url override, got %q", cfg.TMDB.BaseURL)
	}
	if cfg.Workflow.HeartbeatInterval != 20 {
		t.Fatalf("expected heartbeat interval 20, got %d", cfg.Workflow.HeartbeatInterval)
	}
	if cfg.Workflow.HeartbeatTimeout != 200 {
		t.Fatalf("expected heartbeat timeout 200, got %d", cfg.Workflow.HeartbeatTimeout)
	}
}

func TestEnvVarOverridesConfigFileForAPIKeys(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "spindle.toml")

	// Write config file with API keys
	type payload struct {
		TMDB struct {
			APIKey string `toml:"api_key"`
		} `toml:"tmdb"`
		Jellyfin struct {
			APIKey string `toml:"api_key"`
		} `toml:"jellyfin"`
		Subtitles struct {
			OpenSubtitlesAPIKey    string `toml:"opensubtitles_api_key"`
			OpenSubtitlesUserToken string `toml:"opensubtitles_user_token"`
			WhisperXHuggingFace    string `toml:"whisperx_hf_token"`
		} `toml:"subtitles"`
		LLM struct {
			APIKey string `toml:"api_key"`
		} `toml:"llm"`
	}
	custom := payload{}
	custom.TMDB.APIKey = "file-tmdb"
	custom.Jellyfin.APIKey = "file-jellyfin"
	custom.Subtitles.OpenSubtitlesAPIKey = "file-opensub"
	custom.Subtitles.OpenSubtitlesUserToken = "file-opensub-token"
	custom.Subtitles.WhisperXHuggingFace = "file-hf"
	custom.LLM.APIKey = "file-preset"

	data, err := toml.Marshal(custom)
	if err != nil {
		t.Fatalf("marshal custom config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	// Set env vars that should override
	t.Setenv("TMDB_API_KEY", "env-tmdb")
	t.Setenv("JELLYFIN_API_KEY", "env-jellyfin")
	t.Setenv("OPENSUBTITLES_API_KEY", "env-opensub")
	t.Setenv("OPENSUBTITLES_USER_TOKEN", "env-opensub-token")
	t.Setenv("HF_TOKEN", "env-hf")
	t.Setenv("OPENROUTER_API_KEY", "env-preset")

	cfg, _, _, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	// Verify env vars override config file values
	if cfg.TMDB.APIKey != "env-tmdb" {
		t.Errorf("expected TMDB key from env, got %q", cfg.TMDB.APIKey)
	}
	if cfg.Jellyfin.APIKey != "env-jellyfin" {
		t.Errorf("expected Jellyfin key from env, got %q", cfg.Jellyfin.APIKey)
	}
	if cfg.Subtitles.OpenSubtitlesAPIKey != "env-opensub" {
		t.Errorf("expected OpenSubtitles key from env, got %q", cfg.Subtitles.OpenSubtitlesAPIKey)
	}
	if cfg.Subtitles.OpenSubtitlesUserToken != "env-opensub-token" {
		t.Errorf("expected OpenSubtitles token from env, got %q", cfg.Subtitles.OpenSubtitlesUserToken)
	}
	if cfg.Subtitles.WhisperXHuggingFace != "env-hf" {
		t.Errorf("expected HuggingFace token from env, got %q", cfg.Subtitles.WhisperXHuggingFace)
	}
	if cfg.LLM.APIKey != "env-preset" {
		t.Errorf("expected LLM key from env, got %q", cfg.LLM.APIKey)
	}
}

func TestCreateSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.toml")
	if err := config.CreateSample(path); err != nil {
		t.Fatalf("CreateSample failed: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	if !strings.Contains(string(contents), "your_tmdb_api_key_here") {
		t.Fatalf("sample config missing placeholder TMDB key: %s", contents)
	}

	// Validate it decodes
	var cfg config.Config
	if err := toml.Unmarshal(contents, &cfg); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}

	// On Windows join uses backslashes; skip path expectation specifics when running there to avoid
	// differences in drive letters during CI.
	if runtime.GOOS != "windows" {
		if !strings.Contains(cfg.Paths.StagingDir, "spindle") {
			t.Fatalf("expected staging dir to contain spindle, got %q", cfg.Paths.StagingDir)
		}
	}
}

func TestValidateDetectsInvalidValues(t *testing.T) {
	cfg := config.Default()
	cfg.TMDB.APIKey = "key"
	cfg.MakeMKV.RipTimeout = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for non-positive timeout")
	}

	cfg = config.Default()
	cfg.TMDB.APIKey = "key"
	cfg.Workflow.HeartbeatInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for heartbeat interval")
	}

	cfg = config.Default()
	cfg.TMDB.APIKey = "key"
	cfg.Workflow.HeartbeatTimeout = cfg.Workflow.HeartbeatInterval
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when timeout <= interval")
	}

	cfg = config.Default()
	cfg.TMDB.APIKey = "key"
	cfg.Subtitles.OpenSubtitlesEnabled = true
	cfg.Subtitles.OpenSubtitlesAPIKey = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when OpenSubtitles enabled without API key")
	}

	cfg = config.Default()
	cfg.TMDB.APIKey = "key"
	cfg.Subtitles.OpenSubtitlesEnabled = true
	cfg.Subtitles.OpenSubtitlesAPIKey = "abc"
	cfg.Subtitles.OpenSubtitlesUserAgent = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when OpenSubtitles enabled without user agent")
	}

	cfg = config.Default()
	cfg.TMDB.APIKey = "key"
	cfg.Subtitles.OpenSubtitlesEnabled = true
	cfg.Subtitles.OpenSubtitlesAPIKey = "abc"
	cfg.Subtitles.OpenSubtitlesUserAgent = "Spindle/test"
	cfg.Subtitles.OpenSubtitlesLanguages = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when OpenSubtitles enabled without languages")
	}
}
