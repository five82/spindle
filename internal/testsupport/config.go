package testsupport

import (
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/config"
)

// ConfigOption allows callers to customize the generated test configuration.
type ConfigOption func(*configBuilder)

type configBuilder struct {
	t       testing.TB
	baseDir string
	cfg     *config.Config
}

// NewConfig produces a config seeded with unique temp directories per test.
// It defaults common fields and applies any provided options.
func NewConfig(t testing.TB, opts ...ConfigOption) *config.Config {
	t.Helper()

	base := t.TempDir()
	cfgVal := config.Default()
	cfgVal.TMDB.APIKey = "test"
	cfgVal.Paths.StagingDir = filepath.Join(base, "staging")
	cfgVal.Paths.LibraryDir = filepath.Join(base, "library")
	cfgVal.Paths.LogDir = filepath.Join(base, "logs")
	cfgVal.Paths.ReviewDir = filepath.Join(base, "review")
	cfgVal.Paths.APIBind = "127.0.0.1:0"
	// Disable KEYDB in tests to avoid 20m network refreshes.
	cfgVal.MakeMKV.KeyDBPath = ""
	cfgVal.MakeMKV.KeyDBDownloadURL = ""
	cfgVal.MakeMKV.KeyDBDownloadTimeout = 1

	builder := &configBuilder{
		t:       t,
		baseDir: base,
		cfg:     &cfgVal,
	}

	for _, opt := range opts {
		opt(builder)
	}

	return builder.cfg
}

// WithTMDBKey sets the TMDB API key on the test config.
func WithTMDBKey(key string) ConfigOption {
	return func(b *configBuilder) {
		b.cfg.TMDB.APIKey = key
	}
}

// WithOpticalDrive overrides the optical drive path on the test config.
func WithOpticalDrive(path string) ConfigOption {
	return func(b *configBuilder) {
		b.cfg.MakeMKV.OpticalDrive = path
	}
}

// WithStubbedBinaries writes stub executables for the provided names and
// prepends them to PATH. If names is empty, the default spindle external
// binaries are stubbed.
func WithStubbedBinaries(names ...string) ConfigOption {
	return func(b *configBuilder) {
		if len(names) == 0 {
			names = []string{"makemkvcon", "drapto", "ffmpeg", "ffprobe", "mediainfo"}
		}
		binDir := filepath.Join(b.baseDir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			b.t.Fatalf("mkdir bin dir: %v", err)
		}
		script := []byte("#!/bin/sh\nexit 0\n")
		for _, name := range names {
			target := filepath.Join(binDir, name)
			if err := os.WriteFile(target, script, 0o755); err != nil {
				b.t.Fatalf("write stub %s: %v", name, err)
			}
		}

		oldPath := os.Getenv("PATH")
		if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath); err != nil {
			b.t.Fatalf("set PATH: %v", err)
		}
		b.t.Cleanup(func() {
			_ = os.Setenv("PATH", oldPath)
		})
	}
}

// WithRipCache enables the rip cache for tests and points it at a temp dir.
func WithRipCache() ConfigOption {
	return func(b *configBuilder) {
		ripDir := filepath.Join(b.baseDir, "ripcache")
		if err := os.MkdirAll(ripDir, 0o755); err != nil {
			b.t.Fatalf("mkdir rip cache: %v", err)
		}
		b.cfg.RipCache.Enabled = true
		b.cfg.RipCache.Dir = ripDir
		b.cfg.RipCache.MaxGiB = 10
	}
}

// BaseDir returns the root temp directory backing the generated config.
func BaseDir(cfg *config.Config) string {
	return filepath.Dir(cfg.Paths.StagingDir)
}
