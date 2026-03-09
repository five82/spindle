package config

import (
	"fmt"
	"os"
)

// EnsureDirectories creates all required and optional directories.
// Required directories (staging, state, review) cause a fatal error on failure.
// Optional directories (library, cache dirs) are best-effort.
func (c *Config) EnsureDirectories() error {
	// Required directories -- fail on error.
	required := []string{
		c.Paths.StagingDir,
		c.Paths.StateDir,
		c.Paths.ReviewDir,
	}
	for _, dir := range required {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create required directory %q: %w", dir, err)
		}
	}

	// Optional directories -- best-effort, don't fail.
	optional := []string{
		c.Paths.LibraryDir,
		c.OpenSubtitlesCacheDir(),
		c.WhisperXCacheDir(),
	}
	if c.RipCache.Enabled {
		optional = append(optional, c.RipCacheDir())
	}
	for _, dir := range optional {
		if dir == "" {
			continue
		}
		_ = os.MkdirAll(dir, 0o755)
	}

	return nil
}
