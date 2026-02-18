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

// RunAll executes all applicable preflight checks for the given config.
// Checks are only run when the corresponding feature is enabled.
func RunAll(ctx context.Context, cfg *config.Config) []Result {
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

	// Preset Decider LLM
	if cfg.PresetDecider.Enabled {
		results = append(results, CheckLLM(ctx, "Preset Decider LLM", cfg.PresetLLM()))
	}

	// Commentary LLM (only when it uses a distinct endpoint from preset decider)
	if cfg.Commentary.Enabled && commentaryUsesDistinctLLM(cfg) {
		results = append(results, CheckLLM(ctx, "Commentary LLM", cfg.CommentaryLLM()))
	}

	return results
}

// commentaryUsesDistinctLLM returns true when the commentary LLM config
// resolves to a different API key or base URL than the preset decider.
// When they're identical, the preset decider check already covers it.
func commentaryUsesDistinctLLM(cfg *config.Config) bool {
	preset := cfg.PresetLLM()
	commentary := cfg.CommentaryLLM()
	return preset.APIKey != commentary.APIKey || preset.BaseURL != commentary.BaseURL
}
