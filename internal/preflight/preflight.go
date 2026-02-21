package preflight

import (
	"context"

	"spindle/internal/config"
)

// Result reports the outcome of a single preflight check.
type Result struct {
	Name   string
	Passed bool
	Detail string
}

// RunFeatureChecks executes preflight checks for enabled features.
// Only features toggled on in the config are checked (Jellyfin, Commentary LLM, etc.).
// System-level dependencies (MakeMKV, FFmpeg) are checked separately via CheckSystemDeps.
func RunFeatureChecks(ctx context.Context, cfg *config.Config) []Result {
	if cfg == nil {
		return nil
	}

	var results []Result

	// Staging directory (always checked)
	results = append(results, CheckDirectoryAccess("Staging directory", cfg.Paths.StagingDir))

	// Library directory (when configured)
	if cfg.Paths.LibraryDir != "" {
		results = append(results, CheckDirectoryAccess("Library directory", cfg.Paths.LibraryDir))
	}

	// Jellyfin
	if cfg.Jellyfin.Enabled {
		results = append(results, CheckJellyfin(ctx, cfg.Jellyfin.URL, cfg.Jellyfin.APIKey))
	}

	// Commentary LLM
	if cfg.Commentary.Enabled {
		results = append(results, CheckLLM(ctx, "Commentary LLM", cfg.CommentaryLLM()))
	}

	return results
}
