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
	if cfg.StagingDir != wantStaging {
		t.Fatalf("unexpected staging dir: got %q want %q", cfg.StagingDir, wantStaging)
	}
	if cfg.LibraryDir != filepath.Join(tempHome, "library") {
		t.Fatalf("unexpected library dir: %q", cfg.LibraryDir)
	}
	if cfg.TMDBAPIKey != "test-key" {
		t.Fatalf("expected TMDB key from env, got %q", cfg.TMDBAPIKey)
	}
	if cfg.TMDBBaseURL != config.Default().TMDBBaseURL {
		t.Fatalf("unexpected TMDB base url: %q", cfg.TMDBBaseURL)
	}
	if cfg.WorkflowWorkerCount != config.Default().WorkflowWorkerCount {
		t.Fatalf("unexpected worker count: %d", cfg.WorkflowWorkerCount)
	}
	if cfg.WorkflowHeartbeatInterval != config.Default().WorkflowHeartbeatInterval {
		t.Fatalf("unexpected heartbeat interval: %d", cfg.WorkflowHeartbeatInterval)
	}
	if cfg.WorkflowHeartbeatTimeout != config.Default().WorkflowHeartbeatTimeout {
		t.Fatalf("unexpected heartbeat timeout: %d", cfg.WorkflowHeartbeatTimeout)
	}

	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	for _, dir := range []string{cfg.StagingDir, cfg.LibraryDir, cfg.LogDir, cfg.ReviewDir} {
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
	t.Setenv("TMDB_API_KEY", "from-env")
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "spindle.toml")

	type payload struct {
		TMDBAPIKey                string `toml:"tmdb_api_key"`
		TMDBBaseURL               string `toml:"tmdb_base_url"`
		MoviesDir                 string `toml:"movies_dir"`
		WorkflowWorkerCount       int    `toml:"workflow_worker_count"`
		WorkflowHeartbeatInterval int    `toml:"workflow_heartbeat_interval"`
		WorkflowHeartbeatTimeout  int    `toml:"workflow_heartbeat_timeout"`
	}
	data, err := toml.Marshal(payload{
		TMDBAPIKey:                "abc123",
		TMDBBaseURL:               "https://example.com/tmdb",
		MoviesDir:                 "custom",
		WorkflowWorkerCount:       4,
		WorkflowHeartbeatInterval: 20,
		WorkflowHeartbeatTimeout:  200,
	})
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
	if cfg.TMDBAPIKey != "abc123" {
		t.Fatalf("expected TMDB key from file, got %q", cfg.TMDBAPIKey)
	}
	if cfg.MoviesDir != "custom" {
		t.Fatalf("expected MoviesDir to be 'custom', got %q", cfg.MoviesDir)
	}
	if cfg.TMDBBaseURL != "https://example.com/tmdb" {
		t.Fatalf("expected TMDB base url override, got %q", cfg.TMDBBaseURL)
	}
	if cfg.WorkflowWorkerCount != 4 {
		t.Fatalf("expected worker count 4, got %d", cfg.WorkflowWorkerCount)
	}
	if cfg.WorkflowHeartbeatInterval != 20 {
		t.Fatalf("expected heartbeat interval 20, got %d", cfg.WorkflowHeartbeatInterval)
	}
	if cfg.WorkflowHeartbeatTimeout != 200 {
		t.Fatalf("expected heartbeat timeout 200, got %d", cfg.WorkflowHeartbeatTimeout)
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
		if !strings.Contains(cfg.StagingDir, "spindle") {
			t.Fatalf("expected staging dir to contain spindle, got %q", cfg.StagingDir)
		}
	}
}

func TestValidateDetectsInvalidValues(t *testing.T) {
	cfg := config.Default()
	cfg.TMDBAPIKey = "key"
	cfg.MaxVersionsToRip = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for max_versions_to_rip <= 0")
	}

	cfg = config.Default()
	cfg.TMDBAPIKey = "key"
	cfg.MakeMKVRipTimeout = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for non-positive timeout")
	}

	cfg = config.Default()
	cfg.TMDBAPIKey = "key"
	cfg.TMDBConfidenceThreshold = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for tmdb confidence threshold")
	}

	cfg = config.Default()
	cfg.TMDBAPIKey = "key"
	cfg.EpisodeMappingStrategy = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid episode mapping strategy")
	}
}
