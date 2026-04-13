package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestLoadNoConfigReturnsDefaults(t *testing.T) {
	// Use a temp directory with no config files and isolate HOME
	// so ~/.config/spindle/config.toml is not found.
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	t.Setenv("HOME", dir)

	// Set TMDB_API_KEY so validation passes.
	t.Setenv("TMDB_API_KEY", "test-key")

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load with no config file should succeed, got: %v", err)
	}

	if cfg.TMDB.APIKey != "test-key" {
		t.Errorf("expected TMDB API key from env, got %q", cfg.TMDB.APIKey)
	}
	if cfg.TMDB.BaseURL != "https://api.themoviedb.org/3" {
		t.Errorf("expected default TMDB base URL, got %q", cfg.TMDB.BaseURL)
	}
	if cfg.TMDB.Language != "en-US" {
		t.Errorf("expected default TMDB language, got %q", cfg.TMDB.Language)
	}
	if cfg.Encoding.SVTAV1Preset != 6 {
		t.Errorf("expected default SVT-AV1 preset 6, got %d", cfg.Encoding.SVTAV1Preset)
	}
	if cfg.MakeMKV.OpticalDrive != "/dev/sr0" {
		t.Errorf("expected default optical drive, got %q", cfg.MakeMKV.OpticalDrive)
	}
	if cfg.MakeMKV.RipTimeout != 14400 {
		t.Errorf("expected default rip timeout 14400, got %d", cfg.MakeMKV.RipTimeout)
	}
	if cfg.Subtitles.MuxIntoMKV != true {
		t.Error("expected mux_into_mkv default true")
	}
	if cfg.Logging.RetentionDays != 60 {
		t.Errorf("expected default retention days 60, got %d", cfg.Logging.RetentionDays)
	}
	if cfg.Subtitles.TranscriptionPrecision != "bf16" {
		t.Errorf("expected default transcription precision bf16, got %q", cfg.Subtitles.TranscriptionPrecision)
	}
	if cfg.Commentary.SimilarityThreshold != 0.92 {
		t.Errorf("expected default similarity threshold 0.92, got %f", cfg.Commentary.SimilarityThreshold)
	}
	if cfg.ContentID.ClearMatchMargin != 0.05 {
		t.Errorf("expected clear_match_margin default 0.05, got %f", cfg.ContentID.ClearMatchMargin)
	}
}

func TestValidateMissingRequiredFields(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	// Do not set TMDB API key.

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should fail with missing required fields")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "tmdb.api_key") {
		t.Errorf("expected error about tmdb.api_key, got: %s", errMsg)
	}
}

func TestValidatePassesWithRequiredFields(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = "/tmp/staging"
	cfg.Paths.StateDir = "/tmp/state"
	cfg.Paths.ReviewDir = "/tmp/review"

	err := cfg.Validate()
	if err != nil {
		t.Fatalf("Validate should pass with all required fields set, got: %v", err)
	}
}

func TestValidateSVTAV1PresetRange(t *testing.T) {
	tests := []struct {
		preset int
		valid  bool
	}{
		{-1, false},
		{0, true},
		{6, true},
		{13, true},
		{14, false},
	}

	for _, tt := range tests {
		cfg := &Config{}
		applyDefaults(cfg)
		cfg.TMDB.APIKey = "test-key"
		cfg.Paths.StagingDir = "/tmp/staging"
		cfg.Paths.StateDir = "/tmp/state"
		cfg.Paths.ReviewDir = "/tmp/review"
		cfg.Encoding.SVTAV1Preset = tt.preset

		err := cfg.Validate()
		if tt.valid && err != nil {
			t.Errorf("preset %d should be valid, got error: %v", tt.preset, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("preset %d should be invalid, got no error", tt.preset)
		}
	}
}

func TestValidateJellyfinConditional(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = "/tmp/staging"
	cfg.Paths.StateDir = "/tmp/state"
	cfg.Paths.ReviewDir = "/tmp/review"
	cfg.Jellyfin.Enabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should fail when jellyfin enabled without url/api_key")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "jellyfin.url") {
		t.Errorf("expected error about jellyfin.url, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "jellyfin.api_key") {
		t.Errorf("expected error about jellyfin.api_key, got: %s", errMsg)
	}
}

func TestValidateSubtitlesTranscriptionConfig(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = "/tmp/staging"
	cfg.Paths.StateDir = "/tmp/state"
	cfg.Paths.ReviewDir = "/tmp/review"
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.TranscriptionDevice = "tpu"
	cfg.Subtitles.TranscriptionPrecision = "fp16"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should fail with invalid transcription settings")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "subtitles.transcription_device") {
		t.Errorf("expected error about subtitles.transcription_device, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "subtitles.transcription_precision") {
		t.Errorf("expected error about subtitles.transcription_precision, got: %s", errMsg)
	}

	cfg.Subtitles.TranscriptionDevice = "auto"
	cfg.Subtitles.TranscriptionPrecision = "bf16"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate should pass with valid transcription settings, got: %v", err)
	}
}

func TestValidateOpenSubtitlesAPIKey(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = "/tmp/staging"
	cfg.Paths.StateDir = "/tmp/state"
	cfg.Paths.ReviewDir = "/tmp/review"
	cfg.Subtitles.OpenSubtitlesEnabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should fail when opensubtitles enabled without api_key")
	}
	if !strings.Contains(err.Error(), "opensubtitles_api_key") {
		t.Errorf("expected error about opensubtitles_api_key, got: %s", err.Error())
	}
}

func TestEnsureDirectoriesCreates(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{}
	cfg.Paths.StagingDir = filepath.Join(dir, "staging")
	cfg.Paths.StateDir = filepath.Join(dir, "state")
	cfg.Paths.ReviewDir = filepath.Join(dir, "review")
	cfg.Paths.LibraryDir = filepath.Join(dir, "library")

	err := cfg.EnsureDirectories()
	if err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	for _, subdir := range []string{"staging", "state", "review", "library"} {
		path := filepath.Join(dir, subdir)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected directory %q to exist: %v", path, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %q to be a directory", path)
		}
	}
}

func TestSampleConfigIsValidTOML(t *testing.T) {
	sample := SampleConfig()
	var parsed map[string]any
	err := toml.Unmarshal([]byte(sample), &parsed)
	if err != nil {
		t.Fatalf("SampleConfig should produce valid TOML, got parse error: %v", err)
	}

	// Should contain all major sections.
	expectedSections := []string{
		"tmdb", "paths", "api", "jellyfin", "library",
		"notifications", "subtitles", "rip_cache", "disc_id_cache",
		"makemkv", "encoding", "llm", "commentary", "content_id", "logging",
	}
	for _, section := range expectedSections {
		if _, ok := parsed[section]; !ok {
			t.Errorf("SampleConfig missing section [%s]", section)
		}
	}
}

func TestAutoDerivedPaths(t *testing.T) {
	cfg := &Config{
		Paths: PathsConfig{
			StateDir: "/var/lib/spindle",
		},
	}

	// QueueDBPath uses state_dir.
	queueDB := cfg.QueueDBPath()
	if queueDB != "/var/lib/spindle/queue.db" {
		t.Errorf("expected queue DB at /var/lib/spindle/queue.db, got %q", queueDB)
	}

	// Cache dirs use XDG_CACHE_HOME.
	t.Setenv("XDG_CACHE_HOME", "/tmp/test-cache")
	// Force os.UserCacheDir to use our env (it reads XDG_CACHE_HOME on Linux).
	opensubDir := cfg.OpenSubtitlesCacheDir()
	transcriptionDir := cfg.TranscriptionCacheDir()
	ripDir := cfg.RipCacheDir()
	discIDPath := cfg.DiscIDCachePath()

	// These use cacheBaseDir() which calls os.UserCacheDir().
	if !strings.Contains(opensubDir, "opensubtitles") {
		t.Errorf("OpenSubtitlesCacheDir should contain 'opensubtitles', got %q", opensubDir)
	}
	if !strings.Contains(transcriptionDir, "transcription") {
		t.Errorf("TranscriptionCacheDir should contain 'transcription', got %q", transcriptionDir)
	}
	if !strings.Contains(ripDir, "rips") {
		t.Errorf("RipCacheDir should contain 'rips', got %q", ripDir)
	}
	if !strings.Contains(discIDPath, "discid_cache.json") {
		t.Errorf("DiscIDCachePath should contain 'discid_cache.json', got %q", discIDPath)
	}

	// Socket and lock use XDG_RUNTIME_DIR.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	sockPath := cfg.SocketPath()
	lockPath := cfg.LockPath()
	if sockPath != "/run/user/1000/spindle.sock" {
		t.Errorf("expected socket at /run/user/1000/spindle.sock, got %q", sockPath)
	}
	if lockPath != "/run/user/1000/spindle.lock" {
		t.Errorf("expected lock at /run/user/1000/spindle.lock, got %q", lockPath)
	}
}

func TestSocketPathFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	cfg := &Config{}
	sockPath := cfg.SocketPath()
	if sockPath != "/tmp/spindle.sock" {
		t.Errorf("expected socket fallback to /tmp/spindle.sock, got %q", sockPath)
	}
}

func TestEnvironmentVariableOverrides(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	t.Setenv("HOME", dir)

	t.Setenv("TMDB_API_KEY", "tmdb-from-env")
	t.Setenv("JELLYFIN_API_KEY", "jf-from-env")
	t.Setenv("OPENROUTER_API_KEY", "or-from-env")
	t.Setenv("SPINDLE_API_TOKEN", "api-from-env")
	t.Setenv("OPENSUBTITLES_API_KEY", "os-from-env")
	t.Setenv("OPENSUBTITLES_USER_TOKEN", "os-user-from-env")

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.TMDB.APIKey != "tmdb-from-env" {
		t.Errorf("TMDB API key not set from env: %q", cfg.TMDB.APIKey)
	}
	if cfg.Jellyfin.APIKey != "jf-from-env" {
		t.Errorf("Jellyfin API key not set from env: %q", cfg.Jellyfin.APIKey)
	}
	if cfg.LLM.APIKey != "or-from-env" {
		t.Errorf("LLM API key not set from env: %q", cfg.LLM.APIKey)
	}
	if cfg.API.Token != "api-from-env" {
		t.Errorf("API token not set from env: %q", cfg.API.Token)
	}
	if cfg.Subtitles.OpenSubtitlesAPIKey != "os-from-env" {
		t.Errorf("OpenSubtitles API key not set from env: %q", cfg.Subtitles.OpenSubtitlesAPIKey)
	}
	if cfg.Subtitles.OpenSubtitlesUserToken != "os-user-from-env" {
		t.Errorf("OpenSubtitles user token not set from env: %q", cfg.Subtitles.OpenSubtitlesUserToken)
	}
}

func TestLoadFromExplicitPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	content := `
[tmdb]
api_key = "from-file"
language = "de-DE"

[encoding]
svt_av1_preset = 4
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath, nil)
	if err != nil {
		t.Fatalf("Load from explicit path failed: %v", err)
	}

	if cfg.TMDB.APIKey != "from-file" {
		t.Errorf("expected TMDB API key from file, got %q", cfg.TMDB.APIKey)
	}
	if cfg.TMDB.Language != "de-DE" {
		t.Errorf("expected language de-DE, got %q", cfg.TMDB.Language)
	}
	if cfg.Encoding.SVTAV1Preset != 4 {
		t.Errorf("expected preset 4, got %d", cfg.Encoding.SVTAV1Preset)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	content := `
[tmdb]
api_key = "from-file"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TMDB_API_KEY", "from-env")

	cfg, err := Load(configPath, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.TMDB.APIKey != "from-env" {
		t.Errorf("env should override file value, got %q", cfg.TMDB.APIKey)
	}
}

func TestMakeMKVRipTimeoutValidation(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = "/tmp/staging"
	cfg.Paths.StateDir = "/tmp/state"
	cfg.Paths.ReviewDir = "/tmp/review"
	cfg.MakeMKV.RipTimeout = -1

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should fail with negative rip_timeout")
	}
	if !strings.Contains(err.Error(), "rip_timeout") {
		t.Errorf("expected error about rip_timeout, got: %s", err.Error())
	}
}

func TestMakeMKVMinTitleLengthValidation(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = "/tmp/staging"
	cfg.Paths.StateDir = "/tmp/state"
	cfg.Paths.ReviewDir = "/tmp/review"
	cfg.MakeMKV.MinTitleLength = -5

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should fail with negative min_title_length")
	}
	if !strings.Contains(err.Error(), "min_title_length") {
		t.Errorf("expected error about min_title_length, got: %s", err.Error())
	}
}

func TestValidateCRFRange(t *testing.T) {
	tests := []struct {
		name  string
		sd    int
		hd    int
		uhd   int
		valid bool
	}{
		{"all zero (unset)", 0, 0, 0, true},
		{"valid values", 24, 26, 26, true},
		{"boundary 63", 63, 63, 63, true},
		{"sd too high", 64, 26, 26, false},
		{"hd negative", 24, -1, 26, false},
		{"uhd too high", 24, 26, 64, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{}
			applyDefaults(cfg)
			cfg.TMDB.APIKey = "test-key"
			cfg.Paths.StagingDir = "/tmp/staging"
			cfg.Paths.StateDir = "/tmp/state"
			cfg.Paths.ReviewDir = "/tmp/review"
			cfg.Encoding.CRFSD = tt.sd
			cfg.Encoding.CRFHD = tt.hd
			cfg.Encoding.CRFUHD = tt.uhd

			err := cfg.Validate()
			if tt.valid && err != nil {
				t.Errorf("should be valid, got error: %v", err)
			}
			if !tt.valid && err == nil {
				t.Error("should be invalid, got no error")
			}
		})
	}
}

func TestLoadContentIDDefaultsAndOverride(t *testing.T) {
	dir := t.TempDir()

	t.Run("default when absent", func(t *testing.T) {
		configPath := filepath.Join(dir, "contentid-default.toml")
		content := `
[tmdb]
api_key = "from-file"
`
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := Load(configPath, nil)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if cfg.ContentID.ClearMatchMargin != 0.05 {
			t.Fatalf("expected clear_match_margin default 0.05, got %f", cfg.ContentID.ClearMatchMargin)
		}
	})

	t.Run("explicit override preserved", func(t *testing.T) {
		configPath := filepath.Join(dir, "contentid-override.toml")
		content := `
[tmdb]
api_key = "from-file"

[content_id]
clear_match_margin = 0.08
`
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := Load(configPath, nil)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if cfg.ContentID.ClearMatchMargin != 0.08 {
			t.Fatalf("expected explicit clear_match_margin to be preserved, got %f", cfg.ContentID.ClearMatchMargin)
		}
	})
}

func TestLoadFromExplicitPathWithCRF(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	content := `
[tmdb]
api_key = "from-file"

[encoding]
svt_av1_preset = 5
crf_sd = 22
crf_hd = 28
crf_uhd = 30
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Encoding.CRFSD != 22 {
		t.Errorf("expected crf_sd 22, got %d", cfg.Encoding.CRFSD)
	}
	if cfg.Encoding.CRFHD != 28 {
		t.Errorf("expected crf_hd 28, got %d", cfg.Encoding.CRFHD)
	}
	if cfg.Encoding.CRFUHD != 30 {
		t.Errorf("expected crf_uhd 30, got %d", cfg.Encoding.CRFUHD)
	}
}

func TestSourcePathPopulated(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	content := `
[tmdb]
api_key = "test-key"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.SourcePath == "" {
		t.Error("SourcePath should be set when loading from explicit path")
	}
	if !filepath.IsAbs(cfg.SourcePath) {
		t.Errorf("SourcePath should be absolute, got %q", cfg.SourcePath)
	}
}

func TestSourcePathEmptyForDefaults(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	t.Setenv("HOME", dir)
	t.Setenv("TMDB_API_KEY", "test-key")

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.SourcePath != "" {
		t.Errorf("SourcePath should be empty for defaults-only, got %q", cfg.SourcePath)
	}
}

func TestReloadEncodingHappyPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	content := `
[tmdb]
api_key = "test-key"

[encoding]
svt_av1_preset = 8
crf_hd = 30
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Modify config file on disk.
	updated := `
[tmdb]
api_key = "test-key"

[encoding]
svt_av1_preset = 5
crf_hd = 28
crf_uhd = 32
`
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	enc, err := ReloadEncoding(cfg)
	if err != nil {
		t.Fatalf("ReloadEncoding failed: %v", err)
	}

	if enc.SVTAV1Preset != 5 {
		t.Errorf("expected reloaded preset 5, got %d", enc.SVTAV1Preset)
	}
	if enc.CRFHD != 28 {
		t.Errorf("expected reloaded crf_hd 28, got %d", enc.CRFHD)
	}
	if enc.CRFUHD != 32 {
		t.Errorf("expected reloaded crf_uhd 32, got %d", enc.CRFUHD)
	}
	if enc.CRFSD != 0 {
		t.Errorf("expected crf_sd 0 (unset), got %d", enc.CRFSD)
	}
}

func TestReloadEncodingNoSourcePath(t *testing.T) {
	cfg := &Config{}
	cfg.Encoding.SVTAV1Preset = 7

	enc, err := ReloadEncoding(cfg)
	if err != nil {
		t.Fatalf("ReloadEncoding should succeed with empty SourcePath, got: %v", err)
	}
	if enc.SVTAV1Preset != 7 {
		t.Errorf("should return existing config, got preset %d", enc.SVTAV1Preset)
	}
}

func TestReloadEncodingFileNotFound(t *testing.T) {
	cfg := &Config{
		SourcePath: "/nonexistent/config.toml",
	}
	cfg.Encoding.SVTAV1Preset = 7

	enc, err := ReloadEncoding(cfg)
	if err == nil {
		t.Fatal("ReloadEncoding should return error for missing file")
	}
	if enc.SVTAV1Preset != 7 {
		t.Errorf("should return existing config on error, got preset %d", enc.SVTAV1Preset)
	}
}

func TestReloadEncodingInvalidCRF(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	content := `
[encoding]
crf_hd = 99
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{SourcePath: configPath}
	cfg.Encoding.CRFHD = 26

	enc, err := ReloadEncoding(cfg)
	if err == nil {
		t.Fatal("ReloadEncoding should return error for invalid CRF")
	}
	if !strings.Contains(err.Error(), "crf_hd") {
		t.Errorf("error should mention crf_hd, got: %s", err.Error())
	}
	if enc.CRFHD != 26 {
		t.Errorf("should return existing config on error, got crf_hd %d", enc.CRFHD)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"~/foo", filepath.Join(home, "foo")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
	}

	for _, tt := range tests {
		got, err := expandHome(tt.input)
		if err != nil {
			t.Errorf("expandHome(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
